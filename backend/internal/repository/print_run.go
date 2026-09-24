package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/constants"
	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"gorm.io/gorm"
)

var (
	// ErrReleaseConflict means the same interval was already recorded, which is
	// exactly what happens when two reviewers submit overlapping passes at the
	// same time: only the first record is kept.
	ErrReleaseConflict = errors.New("同一区间的放行记录已存在，本次提交未生效")
	// ErrReleaseOverflow means the cumulative quantity would exceed the plan.
	ErrReleaseOverflow = errors.New("累计放行数量将超过计划份数")
	// ErrReleaseState means the run is not waiting for partial releases.
	ErrReleaseState = errors.New("批次须处于校样中才能登记分批放行")
)

// PrintRunRepository owns all persistence operations for 印刷批次.
type PrintRunRepository interface {
	List(context.Context, dto.PageQuery) (Page[model.PrintRun], error)
	Get(context.Context, uint) (model.PrintRun, error)
	CreateVersioned(context.Context, *model.PrintRun, string, string, string) error
	UpdateVersioned(context.Context, uint, uint, *model.PrintRun, string, string, string) error
	RecordRelease(context.Context, *model.RunRelease, string) (model.PrintRun, error)
	ListReleases(context.Context, uint) ([]model.RunRelease, error)
	Delete(context.Context, uint) error
	CountByStatus(context.Context) (map[string]int64, error)
}

type printRunRepository struct {
	store *Store[model.PrintRun]
}

func NewPrintRunRepository(db *gorm.DB) PrintRunRepository {
	return &printRunRepository{store: NewStore[model.PrintRun](db)}
}

func (r *printRunRepository) List(ctx context.Context, q dto.PageQuery) (Page[model.PrintRun], error) {
	return r.store.List(ctx, q)
}
func (r *printRunRepository) Get(ctx context.Context, id uint) (model.PrintRun, error) {
	var item model.PrintRun
	err := r.store.db.WithContext(ctx).
		Preload("Revisions", func(db *gorm.DB) *gorm.DB { return db.Order("version DESC") }).
		First(&item, id).Error
	return item, err
}
func (r *printRunRepository) CreateVersioned(ctx context.Context, item *model.PrintRun, actor, requestID, reason string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("Revisions").Create(item).Error; err != nil {
			return err
		}
		return tx.Create(printRunRevision(item, actor, requestID, reason)).Error
	})
}
func (r *printRunRepository) UpdateVersioned(ctx context.Context, id, version uint, item *model.PrintRun, actor, requestID, reason string) error {
	return r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&model.PrintRun{}).Where("id = ? AND version = ?", id, version).
			Select("*").Omit("id", "code", "created_at", "deleted_at", "Revisions").Updates(item)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrVersionConflict
		}
		return tx.Create(printRunRevision(item, actor, requestID, reason)).Error
	})
}

func printRunRevision(item *model.PrintRun, actor, requestID, reason string) *model.PrintRunRevision {
	return &model.PrintRunRevision{
		PrintRunID: item.ID, Version: item.Version, Status: item.Status, Name: item.Name,
		Facility: item.Facility, Owner: item.Owner, Category: item.Category,
		RiskLevel: item.RiskLevel, MetricValue: item.MetricValue, MetricUnit: item.MetricUnit,
		Evidence: item.Evidence, RelatedCode: item.RelatedCode, PlannedQuantity: item.PlannedQuantity,
		Actor: actor, RequestID: requestID, Reason: reason,
	}
}

// RecordRelease appends one partial release entry and advances the cumulative
// counters in a single transaction. The interval is derived from the current
// cumulative quantity; the unique (run, start_no) index plus the conditional
// counter update make concurrent overlapping submissions keep exactly one
// record, and any failure rolls the whole transaction back so the cumulative
// quantity never moves on a rejected attempt.
func (r *printRunRepository) RecordRelease(ctx context.Context, release *model.RunRelease, reason string) (model.PrintRun, error) {
	var run model.PrintRun
	err := r.store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&run, release.PrintRunID).Error; err != nil {
			return err
		}
		if run.Status != string(constants.RunStateProofing) {
			return ErrReleaseState
		}
		remaining := run.PlannedQuantity - run.ReleasedQuantity
		if release.Quantity < 1 || release.Quantity > remaining {
			return fmt.Errorf("%w: 剩余 %d 份，本次提交 %d 份", ErrReleaseOverflow, remaining, release.Quantity)
		}
		release.StartNo = run.ReleasedQuantity + 1
		release.EndNo = run.ReleasedQuantity + release.Quantity
		if err := tx.Create(release).Error; err != nil {
			if isUniqueViolation(err) {
				return ErrReleaseConflict
			}
			return err
		}
		run.ReleasedQuantity += release.Quantity
		if run.ReleasedQuantity >= run.PlannedQuantity {
			run.Status = string(constants.RunStateReleased)
		}
		run.Version++
		run.UpdatedAt = time.Now().UTC()
		result := tx.Model(&model.PrintRun{}).
			Where("id = ? AND version = ? AND released_quantity = ?", run.ID, run.Version-1, release.StartNo-1).
			Updates(map[string]any{
				"released_quantity": run.ReleasedQuantity,
				"status":            run.Status,
				"version":           run.Version,
				"updated_at":        run.UpdatedAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrReleaseConflict
		}
		return tx.Create(printRunRevision(&run, release.Actor, release.RequestID, reason)).Error
	})
	if err != nil {
		return model.PrintRun{}, err
	}
	run.Releases = nil
	run.Revisions = nil
	return run, nil
}

func (r *printRunRepository) ListReleases(ctx context.Context, runID uint) ([]model.RunRelease, error) {
	items := make([]model.RunRelease, 0)
	err := r.store.db.WithContext(ctx).
		Where("print_run_id = ?", runID).
		Order("start_no ASC, id ASC").
		Find(&items).Error
	return items, err
}
func (r *printRunRepository) Delete(ctx context.Context, id uint) error {
	return r.store.Delete(ctx, id)
}
func (r *printRunRepository) CountByStatus(ctx context.Context) (map[string]int64, error) {
	return r.store.CountByStatus(ctx)
}
