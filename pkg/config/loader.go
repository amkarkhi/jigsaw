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

// Loader handles configuration loading and hot-reload
type Loader struct {
	configPath string
	watcher    *fsnotify.Watcher
	logger     zerolog.Logger
	mu         sync.RWMutex
	stopChan   chan struct{}
}

// NewLoader creates a new configuration loader
func NewLoader(logger zerolog.Logger) *Loader {
	return &Loader{
		logger:   logger,
		stopChan: make(chan struct{}),
	}
}

// Load loads configuration from directory
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
	
	// Load tasks
	tasksPath := filepath.Join(configPath, "tasks")
	if err := l.loadTasks(tasksPath, config); err != nil {
		return nil, fmt.Errorf("failed to load tasks: %w", err)
	}
	
	// Load flows
	flowsPath := filepath.Join(configPath, "flows")
	if err := l.loadFlows(flowsPath, config); err != nil {
		return nil, fmt.Errorf("failed to load flows: %w", err)
	}
	
	// Load providers
	providersPath := filepath.Join(configPath, "providers")
	if err := l.loadProviders(providersPath, config); err != nil {
		return nil, fmt.Errorf("failed to load providers: %w", err)
	}
	
	// Load endpoints
	endpointsPath := filepath.Join(configPath, "endpoints")
	if err := l.loadEndpoints(endpointsPath, config); err != nil {
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

// Watch watches for configuration changes and triggers reload
func (l *Loader) Watch(configPath string, onChange func(*types.Config)) error {
	var err error
	l.watcher, err = fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	
	// Watch all subdirectories
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

// watchLoop handles file system events
func (l *Loader) watchLoop(onChange func(*types.Config)) {
	for {
		select {
		case event, ok := <-l.watcher.Events:
			if !ok {
				return
			}
			
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) != 0 {
				l.logger.Info().Str("file", event.Name).Str("op", event.Op.String()).Msg("Configuration file changed")
				
				// Reload configuration
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

// StopWatch stops watching for configuration changes
func (l *Loader) StopWatch() error {
	close(l.stopChan)
	if l.watcher != nil {
		return l.watcher.Close()
	}
	return nil
}

// loadTasks loads all task configurations
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
			Tasks []types.Task `yaml:"tasks"`
		}
		
		if err := yaml.Unmarshal(data, &taskFile); err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}
		
		for _, task := range taskFile.Tasks {
			taskCopy := task
			config.Tasks[task.Name] = &taskCopy
			l.logger.Debug().Str("task", task.Name).Str("file", path).Msg("Task loaded")
		}
		
		return nil
	})
}

// loadFlows loads all flow configurations
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
		
		var flowFile struct {
			Flows []types.Flow `yaml:"flows"`
		}
		
		if err := yaml.Unmarshal(data, &flowFile); err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}
		
		for _, flow := range flowFile.Flows {
			flowCopy := flow
			config.Flows[flow.Name] = &flowCopy
			l.logger.Debug().Str("flow", flow.Name).Str("file", path).Msg("Flow loaded")
		}
		
		return nil
	})
}

// loadProviders loads all provider configurations
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

// loadEndpoints loads all endpoint configurations
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
