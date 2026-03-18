package config

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSkillRegistry(t *testing.T) {
	skills := map[string]*SkillConfig{
		"kubernetes-basics": {Name: "kubernetes-basics", Description: "K8s basics", Body: "# K8s"},
		"networking":        {Name: "networking", Description: "Network skills", Body: "# Net"},
	}

	registry := NewSkillRegistry(skills)

	t.Run("Get existing skill", func(t *testing.T) {
		skill, err := registry.Get("kubernetes-basics")
		require.NoError(t, err)
		assert.Equal(t, "kubernetes-basics", skill.Name)
		assert.Equal(t, "K8s basics", skill.Description)
		assert.Equal(t, "# K8s", skill.Body)
	})

	t.Run("Get nonexistent skill", func(t *testing.T) {
		_, err := registry.Get("nonexistent")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSkillNotFound)
	})

	t.Run("Has skill", func(t *testing.T) {
		assert.True(t, registry.Has("kubernetes-basics"))
		assert.True(t, registry.Has("networking"))
		assert.False(t, registry.Has("nonexistent"))
	})

	t.Run("GetAll returns copy", func(t *testing.T) {
		all := registry.GetAll()
		assert.Len(t, all, 2)

		all["injected"] = &SkillConfig{Name: "injected"}
		assert.False(t, registry.Has("injected"))
	})

	t.Run("Len", func(t *testing.T) {
		assert.Equal(t, 2, registry.Len())
	})

	t.Run("Names returns sorted list", func(t *testing.T) {
		names := registry.Names()
		assert.Equal(t, []string{"kubernetes-basics", "networking"}, names)
	})
}

func TestSkillRegistryEmpty(t *testing.T) {
	registry := NewSkillRegistry(nil)

	assert.Equal(t, 0, registry.Len())
	assert.Empty(t, registry.Names())
	assert.Empty(t, registry.GetAll())
	assert.False(t, registry.Has("anything"))

	_, err := registry.Get("anything")
	assert.ErrorIs(t, err, ErrSkillNotFound)
}

func TestSkillRegistryDefensiveCopy(t *testing.T) {
	original := map[string]*SkillConfig{
		"skill1": {Name: "skill1", Description: "desc"},
	}

	registry := NewSkillRegistry(original)

	original["skill2"] = &SkillConfig{Name: "skill2", Description: "desc"}
	assert.False(t, registry.Has("skill2"), "registry should not be affected by mutations to the original map")
}

func TestSkillRegistryThreadSafety(_ *testing.T) {
	skills := map[string]*SkillConfig{
		"skill1": {Name: "skill1", Description: "desc1"},
		"skill2": {Name: "skill2", Description: "desc2"},
	}

	registry := NewSkillRegistry(skills)

	const goroutines = 100
	var wg sync.WaitGroup

	for range goroutines {
		wg.Go(func() {
			_, _ = registry.Get("skill1")
			_ = registry.Has("skill2")
			_ = registry.GetAll()
			_ = registry.Names()
			_ = registry.Len()
		})
	}

	wg.Wait()
}
