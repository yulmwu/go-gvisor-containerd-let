package handlers

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"sandboxd-o/sandboxd-let/model"
	"sandboxd-o/sandboxd-orch/config"
	"sandboxd-o/sandboxd-orch/service"
	"sandboxd-o/sandboxd-orch/types"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type Handler struct {
	svc           *service.Service
	createLimiter *rate.Limiter
}

func New(svc *service.Service, cfg config.Config) *Handler {
	rps := cfg.CreateRPS
	if rps <= 0 {
		rps = 20
	}

	burst := cfg.CreateBurst
	if burst < 1 {
		burst = 40
	}

	return &Handler{
		svc:           svc,
		createLimiter: rate.NewLimiter(rate.Limit(rps), burst),
	}
}

// Healthz godoc
// @Summary Health check
// @Description Returns orchestrator process health.
// @Tags orchestrator-system
// @Produce json
// @Success 200 {object} HealthResponse
// @Router /healthz [get]
func (h *Handler) Healthz(c *gin.Context) {
	c.JSON(http.StatusOK, HealthResponse{OK: true})
}

// RegisterNode godoc
// @Summary Register node
// @Description Registers or updates a sandboxd node endpoint.
// @Tags orchestrator-node
// @Accept json
// @Produce json
// @Param request body types.RegisterNodeRequest true "Node registration request"
// @Success 200 {object} NodeResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/nodes/register [post]
func (h *Handler) RegisterNode(c *gin.Context) {
	var req types.RegisterNodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	n, err := h.svc.RegisterNode(c.Request.Context(), req, "api")
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
			return
		}

		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, NodeResponse{Node: n})
}

// ListNodes godoc
// @Summary List nodes
// @Description Lists all registered nodes with state and latest resource data.
// @Tags orchestrator-node
// @Produce json
// @Success 200 {object} NodesResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/nodes [get]
func (h *Handler) ListNodes(c *gin.Context) {
	nodes, err := h.svc.ListNodes(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, NodesResponse{Items: nodes})
}

// GetNode godoc
// @Summary Get node
// @Description Returns a single node by name.
// @Tags orchestrator-node
// @Produce json
// @Param name path string true "Node name"
// @Success 200 {object} NodeResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/nodes/{name} [get]
func (h *Handler) GetNode(c *gin.Context) {
	n, err := h.svc.GetNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "node not found"})
			return
		}

		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, NodeResponse{Node: n})
}

// DeleteNode godoc
// @Summary Delete node
// @Description Deletes node registration and detaches related sandbox scheduling metadata.
// @Tags orchestrator-node
// @Produce json
// @Param name path string true "Node name"
// @Success 200 {object} DeleteNodeResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/nodes/{name} [delete]
func (h *Handler) DeleteNode(c *gin.Context) {
	if err := h.svc.DeleteNode(c.Request.Context(), c.Param("name")); err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
			return
		}

		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, DeleteNodeResponse{Deleted: c.Param("name")})
}

// HeartbeatNode godoc
// @Summary Trigger node heartbeat
// @Description Probes sandboxd health and node resources immediately.
// @Tags orchestrator-node
// @Produce json
// @Param name path string true "Node name"
// @Success 200 {object} HeartbeatResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/nodes/{name}/heartbeat [post]
func (h *Handler) HeartbeatNode(c *gin.Context) {
	client, node, err := h.svc.SandboxClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "node not found"})
			return
		}

		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
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

	c.JSON(http.StatusOK, HeartbeatResponse{
		Node:           node,
		Heartbeat:      status,
		Resources:      res,
		HeartbeatError: errString(hbErr),
		StatusError:    errString(stErr),
	})
}

// NodeListSandboxes godoc
// @Summary Proxy list sandboxes
// @Description Proxies sandbox list request to selected node sandboxd.
// @Tags orchestrator-proxy
// @Produce json
// @Param name path string true "Node name"
// @Param cursor query string false "Pagination cursor"
// @Param limit query int false "Page size"
// @Success 200 {object} ProxyResponse
// @Failure 404 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/v1/nodes/{name}/sandboxes [get]
func (h *Handler) NodeListSandboxes(c *gin.Context) {
	client, _, err := h.svc.SandboxOpClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	out, err := client.ListSandboxes(c.Request.Context(), c.Query("cursor"), limit)
	respondProxy(c, out, err)
}

// NodeGetSandbox godoc
// @Summary Proxy get sandbox
// @Description Proxies sandbox detail request to selected node sandboxd.
// @Tags orchestrator-proxy
// @Produce json
// @Param name path string true "Node name"
// @Param id path string true "Sandbox ID"
// @Success 200 {object} ProxyResponse
// @Failure 404 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/v1/nodes/{name}/sandboxes/{id} [get]
func (h *Handler) NodeGetSandbox(c *gin.Context) {
	client, _, err := h.svc.SandboxOpClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	out, err := client.GetSandbox(c.Request.Context(), c.Param("id"))
	respondProxy(c, out, err)
}

// NodeCreateSandbox godoc
// @Summary Proxy create sandbox
// @Description Proxies create-sandbox request directly to selected node sandboxd.
// @Tags orchestrator-proxy
// @Accept json
// @Produce json
// @Param name path string true "Node name"
// @Param request body model.CreateSandboxRequest true "Sandbox create request"
// @Success 200 {object} ProxyResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/v1/nodes/{name}/sandboxes [post]
func (h *Handler) NodeCreateSandbox(c *gin.Context) {
	client, _, err := h.svc.SandboxOpClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	var req model.CreateSandboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	out, err := client.CreateSandbox(c.Request.Context(), req)
	respondProxy(c, out, err)
}

// NodeDeleteSandbox godoc
// @Summary Proxy delete sandbox
// @Description Proxies delete-sandbox request to selected node sandboxd.
// @Tags orchestrator-proxy
// @Produce json
// @Param name path string true "Node name"
// @Param id path string true "Sandbox ID"
// @Success 200 {object} ProxyResponse
// @Failure 404 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/v1/nodes/{name}/sandboxes/{id} [delete]
func (h *Handler) NodeDeleteSandbox(c *gin.Context) {
	client, _, err := h.svc.SandboxOpClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	out, err := client.DeleteSandbox(c.Request.Context(), c.Param("id"))
	respondProxy(c, out, err)
}

// NodeContainerLogs godoc
// @Summary Proxy container logs
// @Description Proxies container logs request to selected node sandboxd.
// @Tags orchestrator-proxy
// @Produce json
// @Param name path string true "Node name"
// @Param id path string true "Sandbox ID"
// @Param container path string true "Container name"
// @Param cursor query string false "Cursor offset"
// @Param limit query int false "Line limit"
// @Success 200 {object} ProxyResponse
// @Failure 404 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/v1/nodes/{name}/sandboxes/{id}/containers/{container}/logs [get]
func (h *Handler) NodeContainerLogs(c *gin.Context) {
	client, _, err := h.svc.SandboxOpClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	out, err := client.GetContainerLogs(c.Request.Context(), c.Param("id"), c.Param("container"), c.Query("cursor"), limit)
	respondProxy(c, out, err)
}

// NodeReconcile godoc
// @Summary Proxy reconcile
// @Description Proxies manual reconcile trigger to selected node sandboxd.
// @Tags orchestrator-proxy
// @Produce json
// @Param name path string true "Node name"
// @Success 200 {object} ProxyResponse
// @Failure 404 {object} ErrorResponse
// @Failure 502 {object} ErrorResponse
// @Router /api/v1/nodes/{name}/reconcile [post]
func (h *Handler) NodeReconcile(c *gin.Context) {
	client, _, err := h.svc.SandboxOpClientForNode(c.Request.Context(), c.Param("name"))
	if err != nil {
		respondNodeErr(c, err)
		return
	}

	out, err := client.Reconcile(c.Request.Context())
	respondProxy(c, out, err)
}

// CreateSandbox godoc
// @Summary Create control-plane sandbox object
// @Description Creates a scheduler-managed sandbox object in orchestrator.
// @Tags orchestrator-sandbox
// @Accept json
// @Produce json
// @Param request body types.CreateSandboxObjectRequest true "Control-plane sandbox request"
// @Success 201 {object} SandboxObjectResponse
// @Failure 400 {object} ErrorResponse
// @Failure 429 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/sandboxes [post]
func (h *Handler) CreateSandbox(c *gin.Context) {
	if h.createLimiter != nil && !h.createLimiter.Allow() {
		c.JSON(http.StatusTooManyRequests, ErrorResponse{Error: "rate limit exceeded for sandbox create"})
		return
	}

	var req types.CreateSandboxObjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
		return
	}

	out, err := h.svc.CreateSandbox(c.Request.Context(), req)
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
			return
		}

		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusCreated, SandboxObjectResponse{Sandbox: out})
}

// ListSandboxes godoc
// @Summary List control-plane sandboxes
// @Description Lists orchestrator sandbox objects and their scheduling status.
// @Tags orchestrator-sandbox
// @Produce json
// @Success 200 {object} SandboxObjectsResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/sandboxes [get]
func (h *Handler) ListSandboxes(c *gin.Context) {
	out, err := h.svc.ListSandboxes(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, SandboxObjectsResponse{Items: out})
}

// GetSandbox godoc
// @Summary Get control-plane sandbox
// @Description Returns one orchestrator sandbox object.
// @Tags orchestrator-sandbox
// @Produce json
// @Param id path string true "Sandbox ID"
// @Success 200 {object} SandboxObjectResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/sandboxes/{id} [get]
func (h *Handler) GetSandbox(c *gin.Context) {
	out, err := h.svc.GetSandbox(c.Request.Context(), c.Param("id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "sandbox not found"})
			return
		}

		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, SandboxObjectResponse{Sandbox: out})
}

// DeleteSandbox godoc
// @Summary Delete control-plane sandbox
// @Description Deletes orchestrator sandbox object and requests runtime deletion on assigned node.
// @Tags orchestrator-sandbox
// @Produce json
// @Param id path string true "Sandbox ID"
// @Success 200 {object} DeleteSandboxResponse
// @Failure 400 {object} ErrorResponse
// @Failure 404 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/sandboxes/{id} [delete]
func (h *Handler) DeleteSandbox(c *gin.Context) {
	if err := h.svc.DeleteSandbox(c.Request.Context(), c.Param("id")); err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, ErrorResponse{Error: err.Error()})
			return
		}

		if errors.Is(err, sql.ErrNoRows) {
			c.JSON(http.StatusNotFound, ErrorResponse{Error: "sandbox not found"})
			return
		}

		c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	c.JSON(http.StatusOK, DeleteSandboxResponse{Deleted: strings.TrimSpace(c.Param("id"))})
}

func respondNodeErr(c *gin.Context, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		c.JSON(http.StatusNotFound, ErrorResponse{Error: "node not found"})
		return
	}

	c.JSON(http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
}

func respondProxy(c *gin.Context, out map[string]any, err error) {
	if err != nil {
		c.JSON(http.StatusBadGateway, ErrorResponse{Error: err.Error()})
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
