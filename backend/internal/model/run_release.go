package model

import "time"

// RunRelease records one partial 放行 for a 印刷批次. Printing is usually
// finished in several passes, so every release captures the copies completed
// this time and the 印刷机台 that produced them. The sequence interval is
// assigned by the server (released + 1 .. released + quantity), which keeps
// later records from overlapping an earlier one.
type RunRelease struct {
	ID              uint      `json:"id" gorm:"primaryKey"`
	PrintRunID      uint      `json:"printRunId" gorm:"not null;uniqueIndex:idx_run_release_interval,priority:1;index"`
	StartSequence   int       `json:"startSequence" gorm:"not null;uniqueIndex:idx_run_release_interval,priority:2"`
	EndSequence     int       `json:"endSequence" gorm:"not null;index"`
	CompletedCopies int       `json:"completedCopies" gorm:"not null"`
	PressCode       string    `json:"pressCode" gorm:"size:64;not null;index"`
	Actor           string    `json:"actor" gorm:"size:80;not null"`
	RequestID       string    `json:"requestId" gorm:"size:80;not null;index"`
	Reason          string    `json:"reason" gorm:"size:500;not null"`
	CreatedAt       time.Time `json:"createdAt" gorm:"index"`
}

func (item RunRelease) TableName() string { return "print_run_releases" }
