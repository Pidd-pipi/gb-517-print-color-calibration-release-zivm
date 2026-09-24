package dto

import (
	"time"

	"github.com/blueship581/print-color-calibration-release/backend/internal/model"
)

// CreatePrintRun is the public write contract for 印刷批次. Status is deliberately
// omitted so callers cannot bypass the service state machine.
type CreatePrintRun struct {
	Code            string    `json:"code" binding:"required,min=2,max=64"`
	Name            string    `json:"name" binding:"required,min=2,max=160"`
	Description     string    `json:"description" binding:"max=1000"`
	Facility        string    `json:"facility" binding:"required,max=120"`
	Owner           string    `json:"owner" binding:"required,max=120"`
	Category        string    `json:"category" binding:"required,max=80"`
	RiskLevel       string    `json:"riskLevel" binding:"required,oneof=low medium high critical"`
	MetricValue     float64   `json:"metricValue"`
	MetricUnit      string    `json:"metricUnit" binding:"max=24"`
	EffectiveAt     time.Time `json:"effectiveAt" binding:"required"`
	Evidence        string    `json:"evidence" binding:"max=2000"`
	RelatedCode     string    `json:"relatedCode" binding:"max=64"`
	PlannedQuantity int64     `json:"plannedQuantity" binding:"required,min=1"`
}

type UpdatePrintRun struct {
	ExpectedVersion uint      `json:"expectedVersion" binding:"required"`
	Name            string    `json:"name" binding:"required,min=2,max=160"`
	Description     string    `json:"description" binding:"max=1000"`
	Facility        string    `json:"facility" binding:"required,max=120"`
	Owner           string    `json:"owner" binding:"required,max=120"`
	Category        string    `json:"category" binding:"required,max=80"`
	RiskLevel       string    `json:"riskLevel" binding:"required,oneof=low medium high critical"`
	MetricValue     float64   `json:"metricValue"`
	MetricUnit      string    `json:"metricUnit" binding:"max=24"`
	EffectiveAt     time.Time `json:"effectiveAt" binding:"required"`
	Evidence        string    `json:"evidence" binding:"max=2000"`
	RelatedCode     string    `json:"relatedCode" binding:"max=64"`
	PlannedQuantity int64     `json:"plannedQuantity" binding:"required,min=1"`
}

// CreateRunRelease records one partial release pass: how many copies were
// completed this time and on which press unit. The interval itself is derived
// server-side from the cumulative released quantity.
type CreateRunRelease struct {
	Quantity  int64  `json:"quantity" binding:"required,min=1"`
	PressUnit string `json:"pressUnit" binding:"required,min=2,max=120"`
	Note      string `json:"note" binding:"max=500"`
}

// RunReleaseResult returns the stored entry plus the cumulative counters the
// release page must show after every attempt.
type RunReleaseResult struct {
	Release           model.RunRelease `json:"release"`
	Run               model.PrintRun   `json:"run"`
	ReleasedQuantity  int64            `json:"releasedQuantity"`
	RemainingQuantity int64            `json:"remainingQuantity"`
}
