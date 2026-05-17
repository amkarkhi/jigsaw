package ui

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"maps"
	"slices"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

//go:embed templates/*
var templates embed.FS

// WebUI provides a web-based user interface for Jigsaw
type WebUI struct {
	config        *types.Config
	logicRegistry []string
	logicInfo     map[string]map[string]any // Enhanced logic handler info
	router        *gin.Engine
	logger        zerolog.Logger
}

// NewWebUI creates a new web UI instance
func NewWebUI(config *types.Config, logicRegistry []string, logger zerolog.Logger) *WebUI {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	ui := &WebUI{
		config:        config,
		logicRegistry: logicRegistry,
		logicInfo:     make(map[string]map[string]any),
		router:        router,
		logger:        logger,
	}

	ui.setupRoutes()
	return ui
}

func (w *WebUI) setupRoutes() {
	// Serve static HTML
	w.router.GET("/", w.handleIndex)

	// API endpoints
	api := w.router.Group("/api")
	{
		api.GET("/overview", w.handleOverview)
		api.GET("/flows", w.handleFlows)
		api.GET("/flows/:name", w.handleFlowDetail)
		api.GET("/tasks", w.handleTasks)
		api.GET("/tasks/:name", w.handleTaskDetail)
		api.GET("/providers", w.handleProviders)
		api.GET("/endpoints", w.handleEndpoints)
		api.GET("/logic", w.handleLogicRegistry)
		api.GET("/health", w.handleHealth)
	}
}

// Start starts the web UI server
func (w *WebUI) Start(port int) error {
	w.logger.Info().Int("port", port).Str("url", fmt.Sprintf("http://localhost:%d", port)).Msg("Starting Web UI")
	return w.router.Run(fmt.Sprintf(":%d", port))
}

func (w *WebUI) handleIndex(c *gin.Context) {
	tmpl, err := template.ParseFS(templates, "templates/index.html")
	if err != nil {
		c.String(500, "Error loading template: %v", err)
		return
	}

	data := map[string]any{
		"Title": "Jigsaw Configuration Manager",
	}

	tmpl.Execute(c.Writer, data)
}

func (w *WebUI) handleOverview(c *gin.Context) {
	// Count unimplemented logic
	unimplemented := 0
	for _, task := range w.config.Tasks {
		found := slices.Contains(w.logicRegistry, task.Logic)
		if !found {
			unimplemented++
		}
	}

	c.JSON(200, gin.H{
		"flows":                 len(w.config.Flows),
		"tasks":                 len(w.config.Tasks),
		"providers":             len(w.config.Providers),
		"endpoints":             len(w.config.Endpoints),
		"logic_handlers":        len(w.logicRegistry),
		"unimplemented_logic":   unimplemented,
		"has_flows":             len(w.config.Flows) > 0,
		"has_tasks":             len(w.config.Tasks) > 0,
		"has_logic":             len(w.logicRegistry) > 0,
		"all_logic_implemented": unimplemented == 0 && len(w.config.Tasks) > 0,
	})
}

func (w *WebUI) handleFlows(c *gin.Context) {
	flows := make([]map[string]any, 0, len(w.config.Flows))

	for name, flow := range w.config.Flows {
		flows = append(flows, map[string]any{
			"name":        name,
			"description": flow.Description,
			"task_count":  len(flow.Tasks),
			"inherits":    flow.Inherits,
		})
	}

	c.JSON(200, flows)
}

func (w *WebUI) handleFlowDetail(c *gin.Context) {
	name := c.Param("name")
	flow, ok := w.config.Flows[name]
	if !ok {
		c.JSON(404, gin.H{"error": "Flow not found"})
		return
	}

	tasks := make([]string, 0, len(flow.Tasks))
	for _, task := range flow.Tasks {
		tasks = append(tasks, task.Name)
	}

	c.JSON(200, gin.H{
		"name":        name,
		"description": flow.Description,
		"tasks":       tasks,
		"inherits":    flow.Inherits,
		"metadata":    flow.Metadata,
	})
}

func (w *WebUI) handleTasks(c *gin.Context) {
	tasks := make([]map[string]any, 0, len(w.config.Tasks))

	for name, task := range w.config.Tasks {
		// Check if logic is implemented
		logicImplemented := slices.Contains(w.logicRegistry, task.Logic)

		tasks = append(tasks, map[string]any{
			"name":              name,
			"description":       task.Description,
			"logic":             task.Logic,
			"logic_implemented": logicImplemented,
			"provider":          task.Provider,
			"input_count":       len(task.Inputs),
			"output_count":      len(task.Outputs),
			"inherits":          task.Inherits,
		})
	}

	c.JSON(200, tasks)
}

func (w *WebUI) handleTaskDetail(c *gin.Context) {
	name := c.Param("name")
	task, ok := w.config.Tasks[name]
	if !ok {
		c.JSON(404, gin.H{"error": "Task not found"})
		return
	}

	// Check if logic is implemented
	logicImplemented := slices.Contains(w.logicRegistry, task.Logic)

	c.JSON(200, gin.H{
		"name":              name,
		"description":       task.Description,
		"logic":             task.Logic,
		"logic_implemented": logicImplemented,
		"provider":          task.Provider,
		"inputs":            task.Inputs,
		"outputs":           task.Outputs,
		"fallback":          task.Fallback,
		"timeout":           task.Timeout,
		"retry":             task.Retry,
		"inherits":          task.Inherits,
		"metadata":          task.Metadata,
	})
}

func (w *WebUI) handleProviders(c *gin.Context) {
	providers := make([]map[string]any, 0, len(w.config.Providers))

	for name, provider := range w.config.Providers {
		providers = append(providers, map[string]any{
			"name":      name,
			"type":      provider.Type,
			"init_mode": provider.InitMode,
			"pool_size": provider.PoolSize,
		})
	}

	c.JSON(200, providers)
}

func (w *WebUI) handleEndpoints(c *gin.Context) {
	endpoints := make([]map[string]any, 0, len(w.config.Endpoints))

	for name, endpoint := range w.config.Endpoints {
		mappings := make([]map[string]any, 0, len(endpoint.Flows))
		for _, mapping := range endpoint.Flows {
			mappings = append(mappings, map[string]any{
				"sub":  mapping.Sub,
				"flow": mapping.FlowName,
			})
		}

		endpoints = append(endpoints, map[string]any{
			"name":        name,
			"path":        endpoint.Path,
			"method":      endpoint.Method,
			"description": endpoint.Description,
			"mappings":    mappings,
		})
	}

	c.JSON(200, endpoints)
}

func (w *WebUI) handleLogicRegistry(c *gin.Context) {
	// Create a map of logic handlers with their usage
	logicUsage := make(map[string][]string)

	for _, handler := range w.logicRegistry {
		logicUsage[handler] = make([]string, 0)
	}

	// Find which tasks use each logic handler
	for taskName, task := range w.config.Tasks {
		if task.Logic == "\\" {
			continue
		}
		if tasks, ok := logicUsage[task.Logic]; ok {
			logicUsage[task.Logic] = append(tasks, taskName)
		} else {
			// Logic not implemented
			logicUsage[task.Logic] = []string{taskName}
		}
	}

	handlers := make([]map[string]any, 0)
	for logic, tasks := range logicUsage {
		implemented := slices.Contains(w.logicRegistry, logic)

		handlerInfo := map[string]any{
			"name":        logic,
			"implemented": implemented,
			"used_by":     tasks,
		}

		// Add additional metadata if available
		if info, ok := w.logicInfo[logic]; ok {
			maps.Copy(handlerInfo, info)
		}

		handlers = append(handlers, handlerInfo)
	}

	c.JSON(200, handlers)
}

func (w *WebUI) handleHealth(c *gin.Context) {
	c.JSON(200, gin.H{
		"status": "ok",
		"ui":     "web",
	})
}

// UpdateConfig updates the configuration
func (w *WebUI) UpdateConfig(config *types.Config) {
	w.config = config
}

// UpdateLogicRegistry updates the logic registry
func (w *WebUI) UpdateLogicRegistry(logicRegistry []string) {
	w.logicRegistry = logicRegistry
}

// UpdateLogicInfo updates the logic handler metadata
func (w *WebUI) UpdateLogicInfo(logicInfo map[string]map[string]any) {
	w.logicInfo = logicInfo
}

// GetConfigJSON returns the configuration as JSON
func (w *WebUI) GetConfigJSON() (string, error) {
	data, err := json.MarshalIndent(w.config, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
