package services

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSystemWarningsService_AddAndGet(t *testing.T) {
	svc := NewSystemWarningsService()

	id := svc.AddWarning(WarningCategoryMCPHealth, "Server unreachable", "connection refused", "kubernetes")
	assert.NotEmpty(t, id)

	warnings := svc.GetWarnings()
	require.Len(t, warnings, 1)
	assert.Equal(t, WarningCategoryMCPHealth, warnings[0].Category)
	assert.Equal(t, "Server unreachable", warnings[0].Message)
	assert.Equal(t, "connection refused", warnings[0].Details)
	assert.Equal(t, "kubernetes", warnings[0].ServerID)
	assert.False(t, warnings[0].CreatedAt.IsZero())
}

func TestSystemWarningsService_ClearByServerID(t *testing.T) {
	svc := NewSystemWarningsService()

	svc.AddWarning(WarningCategoryMCPHealth, "Server unreachable", "", "kubernetes")
	svc.AddWarning(WarningCategoryMCPHealth, "Server unreachable", "", "github")

	assert.Len(t, svc.GetWarnings(), 2)

	// Clear kubernetes warning
	cleared := svc.ClearByServerID(WarningCategoryMCPHealth, "kubernetes")
	assert.True(t, cleared)
	assert.Len(t, svc.GetWarnings(), 1)
	assert.Equal(t, "github", svc.GetWarnings()[0].ServerID)

	// Clear non-existent
	cleared = svc.ClearByServerID(WarningCategoryMCPHealth, "nonexistent")
	assert.False(t, cleared)
}

func TestSystemWarningsService_ReplacesDuplicate(t *testing.T) {
	svc := NewSystemWarningsService()

	svc.AddWarning(WarningCategoryMCPHealth, "First error", "err1", "kubernetes")
	svc.AddWarning(WarningCategoryMCPHealth, "Second error", "err2", "kubernetes")

	// Should have replaced the first warning, not added a second
	warnings := svc.GetWarnings()
	require.Len(t, warnings, 1)
	assert.Equal(t, "Second error", warnings[0].Message)
	assert.Equal(t, "err2", warnings[0].Details)
}

func TestSystemWarningsService_Empty(t *testing.T) {
	svc := NewSystemWarningsService()
	assert.Empty(t, svc.GetWarnings())
}

func TestSystemWarningsService_ThreadSafety(t *testing.T) {
	svc := NewSystemWarningsService()
	var wg sync.WaitGroup

	// Concurrent adds
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc.AddWarning("test", "msg", "", "")
		}()
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = svc.GetWarnings()
		}()
	}

	wg.Wait()
	// Just ensure no panics — exact count doesn't matter for concurrency test
	assert.NotNil(t, svc.GetWarnings())
}
