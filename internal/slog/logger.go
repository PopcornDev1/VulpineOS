package slog

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Level represents log severity.
type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

// String returns the lowercase name of the level.
func (l Level) String() string {
	switch l {
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	default:
		return "unknown"
	}
}

// Logger writes structured JSON log entries.
type Logger struct {
	output    io.Writer
	component string
	fields    map[string]interface{}
	level     Level
	mu        sync.Mutex
}

// New creates a logger for the given component.
func New(component string, output io.Writer) *Logger {
	return &Logger{
		output:    output,
		component: component,
		fields:    make(map[string]interface{}),
		level:     Info,
	}
}

// With returns a new Logger that includes the given key-value field
// in every log entry. The original logger is not modified.
func (l *Logger) With(key string, val interface{}) *Logger {
	l.mu.Lock()
	defer l.mu.Unlock()

	fields := make(map[string]interface{}, len(l.fields)+1)
	for k, v := range l.fields {
		fields[k] = v
	}
	fields[key] = val

	return &Logger{
		output:    l.output,
		component: l.component,
		fields:    fields,
		level:     l.level,
	}
}

// SetLevel sets the minimum log level. Messages below this level are dropped.
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// Debug logs at debug level.
func (l *Logger) Debug(msg string, fields ...interface{}) {
	l.log(Debug, msg, fields)
}

// Info logs at info level.
func (l *Logger) Info(msg string, fields ...interface{}) {
	l.log(Info, msg, fields)
}

// Warn logs at warn level.
func (l *Logger) Warn(msg string, fields ...interface{}) {
	l.log(Warn, msg, fields)
}

// Error logs at error level.
func (l *Logger) Error(msg string, fields ...interface{}) {
	l.log(Error, msg, fields)
}

func (l *Logger) log(level Level, msg string, fields []interface{}) {
	l.mu.Lock()
	currentLevel := l.level
	l.mu.Unlock()

	if level < currentLevel {
		return
	}

	entry := make(map[string]interface{}, len(l.fields)+4+len(fields)/2)
	entry["ts"] = time.Now().UTC().Format(time.RFC3339)
	entry["level"] = level.String()
	entry["component"] = l.component
	entry["msg"] = msg

	// Copy persistent fields
	l.mu.Lock()
	for k, v := range l.fields {
		entry[k] = v
	}
	l.mu.Unlock()

	// Parse variadic key-value pairs
	for i := 0; i+1 < len(fields); i += 2 {
		if key, ok := fields[i].(string); ok {
			entry[key] = fields[i+1]
		}
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')

	l.mu.Lock()
	l.output.Write(data)
	l.mu.Unlock()
}
