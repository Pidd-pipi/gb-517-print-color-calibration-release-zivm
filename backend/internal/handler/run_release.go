package handler

import (
	"net/http"

	"github.com/blueship581/print-color-calibration-release/backend/internal/dto"
	"github.com/blueship581/print-color-calibration-release/backend/internal/service"
	"github.com/blueship581/print-color-calibration-release/backend/internal/util"
	"github.com/gin-gonic/gin"
)

// submitRunRelease is the partial 放行 endpoint: reviewers record 本次完成数 and
// 印刷机台, and the batch only becomes 已放行 once the cumulative copies fill the
// 计划份数.
func submitRunRelease(s service.PrintRunService) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := parseID(c)
		if !ok {
			return
		}
		var input dto.CreateRunRelease
		if err := c.ShouldBindJSON(&input); err != nil {
			util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		item, err := s.SubmitRelease(c.Request.Context(), id, input, actorFromContext(c), requestIDFromContext(c))
		if err != nil {
			handleError(c, err)
			return
		}
		util.OK(c, item)
	}
}
