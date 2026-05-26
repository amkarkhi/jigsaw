package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"
	"gopkg.in/yaml.v3"
)

// Loader handles configuration loading and hot-reload.
type Loader struct {
	configPath string
	watcher    *fsnotify.Watcher
	logger     zerolog.Logger
	mu         sync.RWMutex
	stopChan   chan struct{}
}

// NewLoader creates a new configuration loader.
func NewLoader(logger zerolog.Logger) *Loader {
	return &Loader{
		logger:   logger,
		stopChan: make(chan struct{}),
	}
}

// Load loads configuration from directory.
func (l *Loader) Load(configPath string) (*types.Config, error) {
	l.mu.Lock()
	l.configPath = configPath
	l.mu.Unlock()

	l.logger.Info().Str("path", configPath).Msg("Loading configuration")

	config := &types.Config{
		Tasks:     make(map[string]*types.Task),
		Flows:     make(map[string]*types.Flow),
		Providers: make(map[string]*types.Provider),
		Endpoints: make(map[string]*types.Endpoint),
	}

	if err := l.loadTasks(filepath.Join(configPath, "tasks"), config); err != nil {
		return nil, fmt.Errorf("failed to load tasks: %w", err)
	}
	if err := l.loadFlows(filepath.Join(configPath, "flows"), config); err != nil {
		return nil, fmt.Errorf("failed to load flows: %w", err)
	}
	if err := l.loadProviders(filepath.Join(configPath, "providers"), config); err != nil {
		return nil, fmt.Errorf("failed to load providers: %w", err)
	}
	if err := l.loadEndpoints(filepath.Join(configPath, "endpoints"), config); err != nil {
		return nil, fmt.Errorf("failed to load endpoints: %w", err)
	}

	l.logger.Info().
		Int("tasks", len(config.Tasks)).
		Int("flows", len(config.Flows)).
		Int("providers", len(config.Providers)).
		Int("endpoints", len(config.Endpoints)).
		Msg("Configuration loaded successfully")

	return config, nil
}

// Watch watches for configuration changes and triggers reload.
func (l *Loader) Watch(configPath string, onChange func(*types.Config)) error {
	var err error
	l.watcher, err = fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}

	dirs := []string{
		filepath.Join(configPath, "tasks"),
		filepath.Join(configPath, "flows"),
		filepath.Join(configPath, "providers"),
		filepath.Join(configPath, "endpoints"),
	}

	for _, dir := range dirs {
		if err := l.watcher.Add(dir); err != nil {
			l.logger.Warn().Str("dir", dir).Err(err).Msg("Failed to watch directory")
		} else {
			l.logger.Info().Str("dir", dir).Msg("Watching directory for changes")
		}
	}

	go l.watchLoop(onChange)
	return nil
}

// watchLoop handles file system events.
func (l *Loader) watchLoop(onChange func(*types.Config)) {
	for {
		select {
		case event, ok := <-l.watcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) != 0 {
				l.logger.Info().Str("file", event.Name).Str("op", event.Op.String()).Msg("Configuration file changed")

				l.mu.RLock()
				configPath := l.configPath
				l.mu.RUnlock()

				config, err := l.Load(configPath)
				if err != nil {
					l.logger.Error().Err(err).Msg("Failed to reload configuration")
					continue
				}
				l.logger.Info().Msg("Configuration reloaded successfully")
				onChange(config)
			}

		case err, ok := <-l.watcher.Errors:
			if !ok {
				return
			}
			l.logger.Error().Err(err).Msg("Watcher error")

		case <-l.stopChan:
			return
		}
	}
}

// StopWatch stops watching for configuration changes.
func (l *Loader) StopWatch() error {
	close(l.stopChan)
	if l.watcher != nil {
		return l.watcher.Close()
	}
	return nil
}

// rawTask is the intermediate YAML representation used during loading. It lets
// us detect the forbidden inputs:/outputs: keys and surface a clear error.
type rawTask struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Version     string         `yaml:"version"`
	Inherits    string         `yaml:"inherits"`
	Params      map[string]any `yaml:"params"`
	Provider    string         `yaml:"provider"`
	Fallback    *types.Fallback `yaml:"fallback"`
	Logic       string         `yaml:"logic"`
	Timeout     int            `yaml:"timeout"`
	Retry       int            `yaml:"retry"`
	Metadata    map[string]any `yaml:"metadata"`
	Wrapper     *types.WrapperRef `yaml:"wrapper"`

	// Forbidden legacy fields — presence triggers a load error.
	Inputs  yaml.Node `yaml:"inputs"`
	Outputs yaml.Node `yaml:"outputs"`
}

func (r *rawTask) toTask() (*types.Task, error) {
	if r.Inputs.Kind != 0 {
		return nil, fmt.Errorf("task %q declares 'inputs:' which is no longer supported; "+
			"remove it and declare input/output schemas on the LogicHandler, then use 'bind:' in flows to wire inputs", r.Name)
	}
	if r.Outputs.Kind != 0 {
		return nil, fmt.Errorf("task %q declares 'outputs:' which is no longer supported; "+
			"remove it and declare input/output schemas on the LogicHandler, then use 'as:' in flows to rename outputs", r.Name)
	}
	return &types.Task{
		Name:        r.Name,
		Description: r.Description,
		Version:     r.Version,
		Inherits:    r.Inherits,
		Params:      r.Params,
		Provider:    r.Provider,
		Fallback:    r.Fallback,
		Logic:       r.Logic,
		Timeout:     r.Timeout,
		Retry:       r.Retry,
		Metadata:    r.Metadata,
		Wrapper:     r.Wrapper,
	}, nil
}

// loadTasks loads all task configurations.
func (l *Loader) loadTasks(tasksPath string, config *types.Config) error {
	if _, err := os.Stat(tasksPath); os.IsNotExist(err) {
		l.logger.Warn().Str("path", tasksPath).Msg("Tasks directory does not exist")
		return nil
	}

	return filepath.Walk(tasksPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		var taskFile struct {
			Tasks []rawTask `yaml:"tasks"`
		}
		if err := yaml.Unmarshal(data, &taskFile); err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}

		for _, raw := range taskFile.Tasks {
			task, err := raw.toTask()
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			config.Tasks[task.Name] = task
			l.logger.Debug().Str("task", task.Name).Str("file", path).Msg("Task loaded")
		}

		return nil
	})
}

// rawFlowFile is the intermediate YAML representation used to detect the
// forbidden flat bind/as shape before converting to types.Flow.
type rawFlowFile struct {
	Flows []rawFlow `yaml:"flows"`
}

type rawFlow struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Version     string         `yaml:"version"`
	Inherits    string         `yaml:"inherits"`
	Tasks       []rawTaskRef   `yaml:"tasks"`
	Metadata    map[string]any `yaml:"metadata"`
}

// rawTaskRef captures all TaskRef fields including the forbidden legacy fields.
type rawTaskRef struct {
	Name      string              `yaml:"name"`
	Overrides []types.TaskOverride `yaml:"overrides"`
	Parallel  *rawParallelBlock   `yaml:"parallel"`
	Bind      yaml.Node           `yaml:"bind"`

	// Forbidden legacy field — presence triggers a load error.
	As yaml.Node `yaml:"as"`
}

type rawParallelBlock struct {
	OnBranchFailure string      `yaml:"on_branch_failure"`
	Branches        []rawBranch `yaml:"branches"`
}

type rawBranch struct {
	Label string       `yaml:"label"`
	Tasks []rawTaskRef `yaml:"tasks"`
}

// toTaskRef converts rawTaskRef to types.TaskRef, detecting forbidden legacy shapes.
func (r *rawTaskRef) toTaskRef(flowName string) (*types.TaskRef, error) {
	if r.As.Kind != 0 {
		return nil, fmt.Errorf(
			"flow %q task %q: 'as:' is no longer accepted; nest it under 'bind:\\n  out:'",
			flowName, r.Name,
		)
	}

	ref := &types.TaskRef{
		Name:      r.Name,
		Overrides: r.Overrides,
	}

	if r.Parallel != nil {
		pb, err := r.Parallel.toParallelBlock(flowName)
		if err != nil {
			return nil, err
		}
		ref.Parallel = pb
	}

	if r.Bind.Kind != 0 {
		b, err := parseBindNode(&r.Bind, flowName, r.Name)
		if err != nil {
			return nil, err
		}
		ref.Bind = b
	}

	return ref, nil
}

// parseBindNode decodes a yaml.Node into a *types.Bind, rejecting the old flat
// map[string]string shape.
func parseBindNode(node *yaml.Node, flowName, taskName string) (*types.Bind, error) {
	// A mapping node with keys that are NOT "in" or "out" means old flat shape.
	// A mapping node with only "in"/"out" keys is the new nested shape.
	if node.Kind == yaml.MappingNode {
		for i := 0; i < len(node.Content)-1; i += 2 {
			key := node.Content[i].Value
			if key != "in" && key != "out" {
				return nil, fmt.Errorf(
					"flow %q task %q: flat 'bind:' map is no longer accepted; nest inputs under 'bind:\\n  in:'",
					flowName, taskName,
				)
			}
		}
	}

	var b types.Bind
	if err := node.Decode(&b); err != nil {
		return nil, fmt.Errorf("flow %q task %q: malformed bind: %w", flowName, taskName, err)
	}
	return &b, nil
}

func (r *rawParallelBlock) toParallelBlock(flowName string) (*types.ParallelBlock, error) {
	pb := &types.ParallelBlock{
		OnBranchFailure: r.OnBranchFailure,
		Branches:        make([]types.Branch, 0, len(r.Branches)),
	}
	for _, rb := range r.Branches {
		branch := types.Branch{Label: rb.Label}
		for _, rt := range rb.Tasks {
			ref, err := rt.toTaskRef(flowName)
			if err != nil {
				return nil, fmt.Errorf("branch %q: %w", rb.Label, err)
			}
			branch.Tasks = append(branch.Tasks, *ref)
		}
		pb.Branches = append(pb.Branches, branch)
	}
	return pb, nil
}

func (rf *rawFlow) toFlow() (*types.Flow, error) {
	flow := &types.Flow{
		Name:        rf.Name,
		Description: rf.Description,
		Version:     rf.Version,
		Inherits:    rf.Inherits,
		Metadata:    rf.Metadata,
		Tasks:       make([]types.TaskRef, 0, len(rf.Tasks)),
	}
	for _, rt := range rf.Tasks {
		ref, err := rt.toTaskRef(rf.Name)
		if err != nil {
			return nil, err
		}
		flow.Tasks = append(flow.Tasks, *ref)
	}
	return flow, nil
}

// loadFlows loads all flow configurations.
func (l *Loader) loadFlows(flowsPath string, config *types.Config) error {
	if _, err := os.Stat(flowsPath); os.IsNotExist(err) {
		l.logger.Warn().Str("path", flowsPath).Msg("Flows directory does not exist")
		return nil
	}

	return filepath.Walk(flowsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		var ff rawFlowFile
		if err := yaml.Unmarshal(data, &ff); err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}

		for _, rf := range ff.Flows {
			flow, err := rf.toFlow()
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			config.Flows[flow.Name] = flow
			l.logger.Debug().Str("flow", flow.Name).Str("file", path).Msg("Flow loaded")
		}

		return nil
	})
}

// loadProviders loads all provider configurations.
func (l *Loader) loadProviders(providersPath string, config *types.Config) error {
	if _, err := os.Stat(providersPath); os.IsNotExist(err) {
		l.logger.Warn().Str("path", providersPath).Msg("Providers directory does not exist")
		return nil
	}

	return filepath.Walk(providersPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		var providerFile struct {
			Providers []types.Provider `yaml:"providers"`
		}
		if err := yaml.Unmarshal(data, &providerFile); err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}

		for _, provider := range providerFile.Providers {
			providerCopy := provider
			config.Providers[provider.Name] = &providerCopy
			l.logger.Debug().Str("provider", provider.Name).Str("file", path).Msg("Provider loaded")
		}

		return nil
	})
}

// loadEndpoints loads all endpoint configurations.
func (l *Loader) loadEndpoints(endpointsPath string, config *types.Config) error {
	if _, err := os.Stat(endpointsPath); os.IsNotExist(err) {
		l.logger.Warn().Str("path", endpointsPath).Msg("Endpoints directory does not exist")
		return nil
	}

	return filepath.Walk(endpointsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || filepath.Ext(path) != ".yml" && filepath.Ext(path) != ".yaml" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		var endpointFile struct {
			Endpoints []types.Endpoint `yaml:"endpoints"`
		}
		if err := yaml.Unmarshal(data, &endpointFile); err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}

		for _, endpoint := range endpointFile.Endpoints {
			endpointCopy := endpoint
			config.Endpoints[endpoint.Name] = &endpointCopy
			l.logger.Debug().Str("endpoint", endpoint.Name).Str("path", endpoint.Path).Str("file", path).Msg("Endpoint loaded")
		}

		return nil
	})
}
