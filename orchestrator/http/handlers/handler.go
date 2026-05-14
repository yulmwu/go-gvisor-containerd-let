package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"sandboxd-o/orchestrator/service"
	"sandboxd-o/orchestrator/types"
	"sandboxd-o/sandboxd/model"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *service.Service
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) RegisterNode(c *gin.Context) {
	var req types.RegisterNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	n, err := h.svc.RegisterNode(c.Request.Context(), req, "api")
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"node": n})
}

func (h *Handler) ListNodes(c *gin.Context) {
	nodes, err := h.svc.ListNodes(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": nodes})
}

func (h *Handler) GetNode(c *gin.Context) {
	n, err := h.svc.GetNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"node": n})
}

func (h *Handler) DeleteNode(c *gin.Context) {
	if err := h.svc.DeleteNode(c.Request.Context(), c.Param("name")); err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"deleted": c.Param("name")})
}

func (h *Handler) HeartbeatNode(c *gin.Context) {
	client, node, err := h.svc.SandboxClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
			return
		}

		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	hbErr := client.Healthz(c.Request.Context())
	st, stErr := client.NodeStatus(c.Request.Context())
	status := "ok"
	if hbErr != nil {
		status = "failed"
	}

	var res any = nil
	if stErr == nil {
		res = st.Resources
	}

	c.JSON(http.StatusOK, gin.H{
		"node":            node,
		"heartbeat":       status,
		"resources":       res,
		"heartbeat_error": errString(hbErr),
		"status_error":    errString(stErr),
	})
}

func (h *Handler) NodeListSandboxes(c *gin.Context) {
	client, _, err := h.svc.SandboxClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	out, err := client.ListSandboxes(c.Request.Context(), c.Query("cursor"), limit)
	respondProxy(c, out, err)
}

func (h *Handler) NodeGetSandbox(c *gin.Context) {
	client, _, err := h.svc.SandboxClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	out, err := client.GetSandbox(c.Request.Context(), c.Param("id"))
	respondProxy(c, out, err)
}

func (h *Handler) NodeCreateSandbox(c *gin.Context) {
	client, _, err := h.svc.SandboxClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	var req model.CreateSandboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	out, err := client.CreateSandbox(c.Request.Context(), req)
	respondProxy(c, out, err)
}

func (h *Handler) NodeDeleteSandbox(c *gin.Context) {
	client, _, err := h.svc.SandboxClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	out, err := client.DeleteSandbox(c.Request.Context(), c.Param("id"))
	respondProxy(c, out, err)
}

func (h *Handler) NodeContainerLogs(c *gin.Context) {
	client, _, err := h.svc.SandboxClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	out, err := client.GetContainerLogs(c.Request.Context(), c.Param("id"), c.Param("container"), c.Query("cursor"), limit)
	respondProxy(c, out, err)
}

func (h *Handler) NodeReconcile(c *gin.Context) {
	client, _, err := h.svc.SandboxClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	out, err := client.Reconcile(c.Request.Context())
	respondProxy(c, out, err)
}

func respondNodeErr(c *gin.Context, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}

	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

func respondProxy(c *gin.Context, out map[string]any, err error) {
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, out)
}

func errString(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}
