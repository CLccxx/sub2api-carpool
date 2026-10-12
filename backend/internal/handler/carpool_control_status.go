package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type carpoolSyncAPI interface {
	ConsoleForMember(context.Context, int64, string) (carpool.ControlConsole, error)
	ControlSyncStatus(context.Context, string, carpool.PageQuery) (carpool.ControlSyncPage, error)
	RetryControlSync(context.Context, int64, string) error
}

func (h *CarpoolControlHandler) MemberConsole(c *gin.Context) {
	actor, ok := controlActor(c, h.MainEnabled() && h.sync != nil)
	if !ok || !noMemberQuery(c) {
		return
	}
	result, err := h.sync.ConsoleForMember(c.Request.Context(), actor, c.Param("id"))
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) SyncStatus(c *gin.Context) {
	if _, ok := controlActor(c, h.MainEnabled() && h.sync != nil); !ok {
		return
	}
	q, ok := memberReadQuery(c, false)
	if !ok {
		return
	}
	result, err := h.sync.ControlSyncStatus(c.Request.Context(), c.Param("id"), carpool.PageQuery{Page: q.Page, PageSize: q.PageSize})
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) RetrySync(c *gin.Context) {
	actor, ok := controlActor(c, h.MainEnabled() && h.sync != nil)
	if !ok || !noMemberQuery(c) || !carpoolJSON(c, &struct{}{}, true) {
		return
	}
	if response.ErrorFrom(c, h.sync.RetryControlSync(c.Request.Context(), actor, c.Param("id"))) {
		return
	}
	response.Success(c, gin.H{"queued": true})
}
