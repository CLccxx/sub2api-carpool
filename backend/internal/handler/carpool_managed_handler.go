package handler

import (
	"github.com/Wei-Shaw/sub2api/internal/carpool"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func managedRequestTarget(c *gin.Context) carpool.ManagedTarget {
	return carpool.ManagedTarget{MainID: c.GetHeader(carpool.ControlHeaderMain), NodeID: c.GetHeader(carpool.ControlHeaderNode), CarID: c.GetHeader(carpool.ControlHeaderCar), InstanceID: c.GetHeader(carpool.ControlHeaderInstance)}
}

func (h *CarpoolControlHandler) ProvisionMember(c *gin.Context) {
	if _, ok := controlActor(c, h.NodeEnabled()); !ok || !carpoolJSON(c, &struct{}{}, false) {
		return
	}
	member, err := h.node.Provision(c.Request.Context(), managedRequestTarget(c), c.Param("subject"))
	carpoolResult(c, member, err)
}

func (h *CarpoolControlHandler) ApplyMemberGrant(c *gin.Context) {
	if _, ok := controlActor(c, h.NodeEnabled()); !ok {
		return
	}
	var grant carpool.ManagedGrant
	if !carpoolJSON(c, &grant, false) {
		return
	}
	if err := h.node.ApplyGrant(c.Request.Context(), managedRequestTarget(c), c.Param("subject"), grant); response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, gin.H{"version": grant.Version, "status": grant.Status})
}
