package handler

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type carpoolControlAPI interface {
	MainID() string
	ConnectionForCar(context.Context, string) (carpool.ControlConnection, error)
	SaveConnection(context.Context, int64, string, carpool.ControlConnectionInput) (carpool.ControlConnection, error)
	CheckConnection(context.Context, int64, string) (carpool.ControlConnection, error)
}

type CarpoolControlHandler struct {
	main       carpoolControlAPI
	node       *carpool.NodeControl
	keys       carpoolManagedKeysAPI
	members    carpoolMemberAPI
	sync       carpoolSyncAPI
	nativeMain *carpool.ControlStore
	quota      carpoolQuotaAPI
}

func NewCarpoolControlHandler(main *carpool.ControlStore, node *carpool.NodeControl, keys *service.CarpoolManagedKeys) *CarpoolControlHandler {
	h := &CarpoolControlHandler{node: node}
	if keys != nil {
		h.keys = keys
	}
	if main != nil {
		h.nativeMain = main
		h.main = main
		h.members = main
		h.sync = main
	}
	return h
}

func (h *CarpoolControlHandler) MainEnabled() bool { return h != nil && h.main != nil }
func (h *CarpoolControlHandler) NodeEnabled() bool { return h != nil && h.node != nil }

// Native admin reads do not have a subject route; pin the delegated request here too.
func (h *CarpoolControlHandler) EnforceControlTarget(c *gin.Context) {
	target := managedRequestTarget(c)
	if target != (carpool.ManagedTarget{}) {
		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		if !h.NodeEnabled() || target.Validate() != nil || target != h.node.Target() {
			response.ErrorFrom(c, carpool.ErrControlTarget)
			c.Abort()
			return
		}
	}
	c.Next()
}

func controlActor(c *gin.Context, enabled bool) (int64, bool) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	if !enabled {
		response.NotFound(c, "Carpool control is disabled")
		return 0, false
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Authentication required")
		return 0, false
	}
	return subject.UserID, true
}

func (h *CarpoolControlHandler) MainInfo(c *gin.Context) {
	if _, ok := controlActor(c, h.MainEnabled()); !ok {
		return
	}
	response.Success(c, gin.H{"main_instance_id": h.main.MainID(), "protocol_version": 1})
}

func (h *CarpoolControlHandler) Connection(c *gin.Context) {
	if _, ok := controlActor(c, h.MainEnabled()); !ok {
		return
	}
	result, err := h.main.ConnectionForCar(c.Request.Context(), c.Param("id"))
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) SaveConnection(c *gin.Context) {
	actor, ok := controlActor(c, h.MainEnabled())
	if !ok {
		return
	}
	var input carpool.ControlConnectionInput
	if !carpoolJSON(c, &input, false) {
		return
	}
	result, err := h.main.SaveConnection(c.Request.Context(), actor, c.Param("id"), input)
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) CheckConnection(c *gin.Context) {
	actor, ok := controlActor(c, h.MainEnabled())
	if !ok || !carpoolJSON(c, &struct{}{}, true) {
		return
	}
	result, err := h.main.CheckConnection(c.Request.Context(), actor, c.Param("id"))
	carpoolResult(c, result, err)
}

func (h *CarpoolControlHandler) NodeInfo(c *gin.Context) {
	if _, ok := controlActor(c, h.NodeEnabled()); !ok {
		return
	}
	response.Success(c, h.node.Info())
}
