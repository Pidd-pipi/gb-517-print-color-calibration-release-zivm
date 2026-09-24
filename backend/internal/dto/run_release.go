package dto

// CreateRunRelease is the write contract for one partial 放行. A reviewer only
// states how many copies were finished this time and on which 印刷机台; the
// sequence interval and the batch status transition are decided by the
// service.
type CreateRunRelease struct {
	ExpectedVersion uint   `json:"expectedVersion" binding:"required"`
	CompletedCopies int    `json:"completedCopies" binding:"required,gt=0,max=100000000"`
	PressCode       string `json:"pressCode" binding:"required,min=2,max=64"`
	Reason          string `json:"reason" binding:"max=500"`
}
