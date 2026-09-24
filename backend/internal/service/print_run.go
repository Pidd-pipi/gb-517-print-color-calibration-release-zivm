package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/constants"
	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
	"github.com/blueship581/print-color-calibration-release/backend/internal/repository"
	"gorm.io/gorm"
)

type PrintRunService interface {
	List(context.Context, dto.PageQuery) (repository.Page[model.PrintRun], error)
	Get(context.Context, uint) (model.PrintRun, error)
	Create(context.Context, dto.CreatePrintRun, string, string) (model.PrintRun, error)
	Update(context.Context, uint, dto.UpdatePrintRun, string, string) (model.PrintRun, error)
	Transition(context.Context, uint, dto.TransitionRequest, string, string, string) (model.PrintRun, error)
	SubmitRelease(context.Context, uint, dto.CreateRunRelease, string, string) (model.PrintRun, error)
	Delete(context.Context, uint, string, string) error
	StatusCounts(context.Context) (map[string]int64, error)
}

type printRunService struct {
	repository repository.PrintRunRepository
	pressUnits repository.PressUnitRepository
	security   SecurityService
}

func NewPrintRunService(repo repository.PrintRunRepository, pressUnits repository.PressUnitRepository, security SecurityService) PrintRunService {
	return &printRunService{repository: repo, pressUnits: pressUnits, security: security}
}

func (s *printRunService) List(ctx context.Context, query dto.PageQuery) (repository.Page[model.PrintRun], error) {
	return s.repository.List(ctx, query)
}

func (s *printRunService) Get(ctx context.Context, id uint) (model.PrintRun, error) {
	return s.repository.Get(ctx, id)
}

func (s *printRunService) Create(ctx context.Context, input dto.CreatePrintRun, actor, requestID string) (model.PrintRun, error) {
	if err := validatePrintRunBusinessFields(input.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.PrintRun{}, err
	}
	if input.PlannedCopies <= 0 {
		return model.PrintRun{}, fmt.Errorf("%w: 计划份数必须为正整数", ErrInvalidInput)
	}
	item := model.PrintRun{
		BaseModel: model.BaseModel{
			Code: strings.ToUpper(strings.TrimSpace(input.Code)), Name: strings.TrimSpace(input.Name),
			Status: model.PrintRunInitialStatus, Version: 1, Description: strings.TrimSpace(input.Description),
		},
		Facility: strings.TrimSpace(input.Facility), Owner: strings.TrimSpace(input.Owner),
		Category: strings.TrimSpace(input.Category), RiskLevel: input.RiskLevel,
		MetricValue: input.MetricValue, MetricUnit: strings.TrimSpace(input.MetricUnit),
		EffectiveAt: input.EffectiveAt.UTC(), Evidence: strings.TrimSpace(input.Evidence),
		RelatedCode:   strings.ToUpper(strings.TrimSpace(input.RelatedCode)),
		PlannedCopies: input.PlannedCopies,
	}
	if err := s.repository.CreateVersioned(ctx, &item, actor, requestID, "created colour configuration"); err != nil {
		return model.PrintRun{}, fmt.Errorf("create 印刷批次: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "create", "PrintRun", item.ID, "", item.Status, "created 印刷批次")
	// Re-read so released/remaining copies and the initial revision are populated.
	return s.repository.Get(ctx, item.ID)
}

func (s *printRunService) Update(ctx context.Context, id uint, input dto.UpdatePrintRun, actor, requestID string) (model.PrintRun, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.PrintRun{}, err
	}
	if err := validatePrintRunBusinessFields(current.Code, input.Name, input.Facility, input.Owner); err != nil {
		return model.PrintRun{}, err
	}
	if input.PlannedCopies < current.ReleasedCopies {
		return model.PrintRun{}, fmt.Errorf("%w: 计划份数不能小于累计放行数 %d", ErrInvalidInput, current.ReleasedCopies)
	}
	current.Name = strings.TrimSpace(input.Name)
	current.Description = strings.TrimSpace(input.Description)
	current.Facility = strings.TrimSpace(input.Facility)
	current.Owner = strings.TrimSpace(input.Owner)
	current.Category = strings.TrimSpace(input.Category)
	current.RiskLevel = input.RiskLevel
	current.MetricValue = input.MetricValue
	current.MetricUnit = strings.TrimSpace(input.MetricUnit)
	current.EffectiveAt = input.EffectiveAt.UTC()
	current.Evidence = strings.TrimSpace(input.Evidence)
	current.RelatedCode = strings.ToUpper(strings.TrimSpace(input.RelatedCode))
	current.PlannedCopies = input.PlannedCopies
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateVersioned(ctx, id, input.ExpectedVersion, &current, actor, requestID, "updated colour configuration"); err != nil {
		return model.PrintRun{}, fmt.Errorf("update 印刷批次: %w", err)
	}
	_ = s.security.Audit(ctx, actor, requestID, "update", "PrintRun", id, current.Status, current.Status, "updated business fields")
	return s.repository.Get(ctx, id)
}

func (s *printRunService) Transition(ctx context.Context, id uint, input dto.TransitionRequest, actor, role, requestID string) (model.PrintRun, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.PrintRun{}, err
	}
	target := strings.TrimSpace(input.Status)
	if (target == string(constants.RunStateReleased) || current.Status == string(constants.RunStateReleased)) && !canReview(role) {
		return model.PrintRun{}, ErrForbidden
	}
	// 批次满数放行只能走分批放行登记，由累计份数驱动状态，避免一次点击把整批
	// 计划数量当作已通过。
	if target == string(constants.RunStateReleased) {
		return model.PrintRun{}, fmt.Errorf("%w: 已放行状态只能由分批放行累计满数触发", ErrInvalidTransition)
	}
	if !constants.CanTransition(constants.PrintRunTransitions, current.Status, target) {
		return model.PrintRun{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.Status, target)
	}
	before := current.Status
	current.Status = target
	current.Version = input.ExpectedVersion + 1
	current.UpdatedAt = time.Now().UTC()
	if err := s.repository.UpdateVersioned(ctx, id, input.ExpectedVersion, &current, actor, requestID, input.Reason); err != nil {
		return model.PrintRun{}, fmt.Errorf("transition 印刷批次: %w", err)
	}
	if err := s.security.Audit(ctx, actor, requestID, "transition", "PrintRun", id, before, target, input.Reason); err != nil {
		return model.PrintRun{}, fmt.Errorf("persist transition audit: %w", err)
	}
	return s.repository.Get(ctx, id)
}

func canReview(role string) bool { return role == model.RoleReviewer || role == model.RoleAdmin }

// SubmitRelease records one partial release. The reviewer provides 本次完成数
// and 印刷机台; the service validates the ledger and the repository applies it
// atomically. The batch stays 校样中 until the cumulative copies equal 计划份数,
// at which point it becomes 已放行.
func (s *printRunService) SubmitRelease(ctx context.Context, id uint, input dto.CreateRunRelease, actor, requestID string) (model.PrintRun, error) {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return model.PrintRun{}, err
	}
	// Note: 校样中 status is enforced inside the locked transaction, not here,
	// so a reviewer submitting at almost the same instant as the filling
	// release always receives a deterministic conflict instead of a status
	// error that depends on read ordering.
	if current.PlannedCopies <= 0 {
		return model.PrintRun{}, fmt.Errorf("%w: 批次缺少计划份数", ErrInvalidInput)
	}
	pressCode := strings.ToUpper(strings.TrimSpace(input.PressCode))
	if pressCode == "" {
		return model.PrintRun{}, fmt.Errorf("%w: 请填写印刷机台", ErrInvalidInput)
	}
	press, err := s.pressUnits.GetByCode(ctx, pressCode)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.PrintRun{}, fmt.Errorf("%w: 印刷机台 %s 不存在", ErrInvalidInput, pressCode)
		}
		return model.PrintRun{}, fmt.Errorf("load 印刷机台: %w", err)
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = fmt.Sprintf("分批放行 %d 份，机台 %s", input.CompletedCopies, press.Code)
	}
	entry := &model.RunRelease{
		CompletedCopies: input.CompletedCopies,
		PressCode:       press.Code,
		Actor:           actor,
		RequestID:       requestID,
		Reason:          reason,
		CreatedAt:       time.Now().UTC(),
	}
	current.UpdatedAt = time.Now().UTC()
	updated, err := s.repository.SubmitRelease(ctx, &current, input.ExpectedVersion, entry, requestID, reason)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrVersionConflict):
			// Two reviewers submitted overlapping intervals with the same
			// version: only the winner commits; the loser gets a 409 and the
			// cumulative quantity is unchanged.
			return model.PrintRun{}, ErrReleaseConflict
		case errors.Is(err, repository.ErrRunNotReleasable):
			return model.PrintRun{}, fmt.Errorf("%w: 只有校样中的批次才能登记放行", ErrInvalidTransition)
		case errors.Is(err, repository.ErrReleaseOverplan):
			return model.PrintRun{}, fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())
		default:
			return model.PrintRun{}, fmt.Errorf("submit 分批放行: %w", err)
		}
	}
	return updated, nil
}

func (s *printRunService) Delete(ctx context.Context, id uint, actor, requestID string) error {
	current, err := s.repository.Get(ctx, id)
	if err != nil {
		return err
	}
	if current.Status != model.PrintRunInitialStatus {
		return ErrLocked
	}
	if err := s.repository.Delete(ctx, id); err != nil {
		return err
	}
	return s.security.Audit(ctx, actor, requestID, "delete", "PrintRun", id, current.Status, "deleted", "soft deleted 印刷批次")
}

func (s *printRunService) StatusCounts(ctx context.Context) (map[string]int64, error) {
	return s.repository.CountByStatus(ctx)
}

func validatePrintRunBusinessFields(code, name, facility, owner string) error {
	if strings.TrimSpace(code) == "" || strings.TrimSpace(name) == "" || strings.TrimSpace(facility) == "" || strings.TrimSpace(owner) == "" {
		return ErrInvalidInput
	}
	return nil
}
