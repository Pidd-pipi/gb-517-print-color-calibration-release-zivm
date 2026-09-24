package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/constants"
	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"gorm.io/gorm"
)

var (
	// ErrReleaseOverplan matches any overplanError, regardless of the detailed
	// Chinese message attached for the reviewer.
	ErrReleaseOverplan  = &overplanError{}
	ErrRunNotReleasable = sentinelError("run is not in proofing state")
)

type sentinelError string

func (e sentinelError) Error() string { return string(e) }

// overplanError carries the exact over-plan figures while still matching
// ErrReleaseOverplan through errors.Is, even when wrapped by fmt.Errorf.
type overplanError struct{ detail string }

func (e *overplanError) Error() string        { return e.detail }
func (e *overplanError) Is(target error) bool { _, ok := target.(*overplanError); return ok }

// PrintRunRepository owns all persistence operations for 印刷批次.
type PrintRunRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.PrintRun], error)
	Get(context.Context, uint) (model.PrintRun, error)
	CreateVersioned(context.Context, *model.PrintRun, string, string, string) error
	UpdateVersioned(context.Context, uint, uint, *model.PrintRun, string, string, string) error
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
	// SubmitRelease appends one partial release and moves the batch to
	// 已放行 once the cumulative copies reach 计划份数. All work happens in one
	// transaction keyed by the run's optimistic version, so concurrent
	// submissions cannot overlap and a failed attempt leaves the cumulative
	// quantity unchanged.
	SubmitRelease(context.Context, *model.PrintRun, uint, *model.RunRelease, string, string) (model.PrintRun, error)
}

type printRunRepository struct {
	store *Store[model.PrintRun]
}

func NewPrintRunRepository(db *gorm.DB) PrintRunRepository {
	return &printRunRepository{store: NewStore[model.PrintRun](db)}
}

func (r *printRunRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.PrintRun], error) {
	page, err := r.store.List(ctx, q)
	if err != nil {
		return page, err
	}
	if err := r.attachReleaseTotals(ctx, page.Items); err != nil {
		return page, err
	}
	return page, nil
}

func (r *printRunRepository) Get(ctx context.Context, id uint) (model.PrintRun, error) {
	var item model.PrintRun
	err := r.store.db.WithContext(ctx).
		Preload("Revisions", func(db *gorm.DB) *gorm.DB { return db.Order("version DESC") }).
		Preload("Releases", func(db *gorm.DB) *gorm.DB { return db.Order("start_sequence ASC") }).
		First(&item, id).Error
	if err != nil {
		return item, err
	}
	applyReleaseTotals(&item)
	return item, nil
}

// attachReleaseTotals fills released/remaining copies from print_run_releases
// without trusting any cached counter on the batch.
func (r *printRunRepository) attachReleaseTotals(ctx context.Context, items []model.PrintRun) error {
	if len(items) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(items))
	for i := range items {
		ids = append(ids, items[i].ID)
	}
	type total struct {
		PrintRunID uint
		Released   int
	}
	totals := make([]total, 0, len(items))
	if err := r.store.db.WithContext(ctx).Model(&model.RunRelease{}).
		Select("print_run_id AS print_run_id, COALESCE(SUM(completed_copies), 0) AS released").
		Where("print_run_id IN ?", ids).Group("print_run_id").Scan(&totals).Error; err != nil {
		return err
	}
	releasedByID := make(map[uint]int, len(totals))
	for _, row := range totals {
		releasedByID[row.PrintRunID] = row.Released
	}
	for i := range items {
		items[i].ReleasedCopies = releasedByID[items[i].ID]
		items[i].RemainingCopies = items[i].PlannedCopies - items[i].ReleasedCopies
	}
	return nil
}

func applyReleaseTotals(item *model.PrintRun) {
	released := 0
	for _, entry := range item.Releases {
		released += entry.CompletedCopies
	}
	item.ReleasedCopies = released
	item.RemainingCopies = item.PlannedCopies - released
}

func (r *printRunRepository) CreateVersioned(ctx context.Context, item *model.PrintRun, actor, requestID, reason string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Revisions", "Releases").Create(item).Error; err != nil {
			return err
		}
		return tx.Create(printRunRevision(item, actor, requestID, reason)).Error
	})
}
func (r *printRunRepository) UpdateVersioned(ctx context.Context, id, version uint, item *model.PrintRun, actor, requestID, reason string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.PrintRun{}).Where("id = ? AND version = ?", id, version).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "Revisions", "Releases").Updates(item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		return tx.Create(printRunRevision(item, actor, requestID, reason)).Error
	})
}

func (r *printRunRepository) SubmitRelease(ctx context.Context, item *model.PrintRun, expectedVersion uint, entry *model.RunRelease, requestID, reason string) (model.PrintRun, error) {
	err := r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Claim the batch version first. On both InnoDB and SQLite this turns
		// simultaneous submissions for the same run into one winner: the loser
		// sees RowsAffected == 0 and rolls back, never inserting an overlapping
		// interval. Only version/deletion are asserted here; status is
		// re-checked against the locked row below so a request arriving just
		// after the batch went 已放行 gets a deterministic conflict.
		claim := tx.Model(&model.PrintRun{}).
			Where("id = ? AND version = ? AND deleted_at IS NULL", item.ID, expectedVersion).
			Update("updated_at", item.UpdatedAt)
		if claim.Error != nil {
			return claim.Error
		}
		if claim.RowsAffected == 0 {
			return ErrVersionConflict
		}
		var locked model.PrintRun
		if err := tx.Select("id", "status", "planned_copies").First(&locked, item.ID).Error; err != nil {
			return err
		}
		if locked.Status != string(constants.RunStateProofing) {
			return ErrRunNotReleasable
		}
		var released int
		if err := tx.Model(&model.RunRelease{}).
			Where("print_run_id = ?", item.ID).
			Select("COALESCE(SUM(completed_copies), 0)").Scan(&released).Error; err != nil {
			return err
		}
		if released+entry.CompletedCopies > locked.PlannedCopies {
			return &overplanError{detail: fmt.Sprintf(
				"本次 %d 份将使累计 %d 份超过计划 %d 份（剩余 %d 份）",
				entry.CompletedCopies, released+entry.CompletedCopies,
				locked.PlannedCopies, locked.PlannedCopies-released)}
		}
		entry.PrintRunID = item.ID
		entry.StartSequence = released + 1
		entry.EndSequence = released + entry.CompletedCopies
		if err := tx.Create(entry).Error; err != nil {
			return err
		}
		target := locked.Status
		if released+entry.CompletedCopies == locked.PlannedCopies {
			target = string(constants.RunStateReleased)
		}
		update := tx.Model(&model.PrintRun{}).
			Where("id = ? AND version = ?", item.ID, expectedVersion).
			Updates(map[string]any{"status": target, "version": expectedVersion + 1, "updated_at": item.UpdatedAt})
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return ErrVersionConflict
		}
		item.PlannedCopies = locked.PlannedCopies
		item.Version = expectedVersion + 1
		item.Status = target
		revision := printRunRevision(item, entry.Actor, requestID, reason)
		if err := tx.Create(revision).Error; err != nil {
			return err
		}
		audit := releaseAuditLogs(item, entry.Actor, requestID, reason, entry, released)
		return tx.Create(&audit).Error
	})
	if err != nil {
		return model.PrintRun{}, err
	}
	return r.Get(ctx, item.ID)
}

func printRunRevision(item *model.PrintRun, actor, requestID, reason string) *model.PrintRunRevision {
	return &model.PrintRunRevision{
		PrintRunID: item.ID, Version: item.Version, Status: item.Status, Name: item.Name,
		Facility: item.Facility, Owner: item.Owner, Category: item.Category,
		RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
		Evidence: item.Evidence, RelatedCode: item.RelatedCode, PlannedCopies: item.PlannedCopies,
		Actor: actor, RequestID: requestID, Reason: reason,
	}
}

// releaseAuditLogs appends a RunRelease ledger entry and, only when the batch
// becomes full, the batch state transition. Both are part of the same
// transaction as the ledger write.
func releaseAuditLogs(item *model.PrintRun, actor, requestID, reason string, entry *model.RunRelease, releasedBefore int) []model.AuditLog {
	detail := reason
	if detail == "" {
		detail = "partial release recorded"
	}
	logs := []model.AuditLog{{
		RequestID: requestID, Actor: actor, Action: "release", EntityType: "RunRelease", EntityID: entry.ID,
		BeforeState: fmt.Sprintf("copies:%d", releasedBefore),
		AfterState:  fmt.Sprintf("copies:%d", releasedBefore+entry.CompletedCopies),
		Detail:      detail, CreatedAt: time.Now().UTC(),
	}}
	if item.Status == string(constants.RunStateReleased) {
		logs = append(logs, model.AuditLog{
			RequestID: requestID, Actor: actor, Action: "transition", EntityType: "PrintRun", EntityID: item.ID,
			BeforeState: string(constants.RunStateProofing), AfterState: item.Status,
			Detail: detail, CreatedAt: time.Now().UTC(),
		})
	}
	return logs
}
func (r *printRunRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *printRunRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}
