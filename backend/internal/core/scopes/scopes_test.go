package scopes

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	t.Run("EmptyDefaultsToReadOnly", func(t *testing.T) {
		got, err := Normalize(nil)
		require.NoError(t, err)
		assert.True(t, ReadOnly(got))
		assert.NotContains(t, got, Full)
	})
	t.Run("DedupesAndSorts", func(t *testing.T) {
		got, err := Normalize([]Scope{ItemsWrite, ItemsRead, ItemsWrite})
		require.NoError(t, err)
		assert.Equal(t, []Scope{ItemsRead, ItemsWrite}, got)
	})
	t.Run("RejectsUnknown", func(t *testing.T) {
		_, err := Normalize([]Scope{"items:read", "bogus:all"})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("RejectsFullCombined", func(t *testing.T) {
		_, err := Normalize([]Scope{Full, ItemsRead})
		require.ErrorIs(t, err, ErrInvalid)
	})
	t.Run("FullAlone", func(t *testing.T) {
		got, err := Normalize([]Scope{Full})
		require.NoError(t, err)
		assert.Equal(t, []Scope{Full}, got)
	})
}

func TestHasAndSubset(t *testing.T) {
	assert.True(t, Has([]Scope{Full}, ItemsDelete))
	assert.True(t, Has([]Scope{ItemsRead}, ItemsRead))
	assert.False(t, Has([]Scope{ItemsRead}, ItemsWrite))
	assert.False(t, Has(nil, ItemsRead))

	assert.True(t, Subset([]Scope{ItemsRead}, []Scope{ItemsRead, ItemsWrite}))
	assert.False(t, Subset([]Scope{ItemsDelete}, []Scope{ItemsRead, ItemsWrite}))
	assert.True(t, Subset([]Scope{ItemsDelete}, []Scope{Full}))
}

func TestPresets(t *testing.T) {
	ro, err := Preset(PresetReadOnly)
	require.NoError(t, err)
	assert.True(t, ReadOnly(ro))

	rw, err := Preset(PresetReadWrite)
	require.NoError(t, err)
	assert.False(t, ReadOnly(rw))
	assert.False(t, Has(rw, ItemsDelete), "read-write must not include delete")

	full, err := Preset(PresetFull)
	require.NoError(t, err)
	assert.True(t, Has(full, ItemsDelete))

	_, err = Preset("nope")
	require.ErrorIs(t, err, ErrInvalid)

	for _, p := range [][]Scope{ro, rw, full} {
		_, err := Normalize(p)
		require.NoError(t, err, "every preset must normalize cleanly")
	}
}

func TestCeilingAndIntersect(t *testing.T) {
	assert.Empty(t, Ceiling(AccessOff))
	assert.Empty(t, Ceiling("garbage"), "unknown levels must fail closed")
	assert.True(t, ReadOnly(Ceiling(AccessRead)))
	assert.False(t, Has(Ceiling(AccessWrite), ItemsDelete))
	assert.True(t, Has(Ceiling(AccessFull), ItemsDelete))
	assert.False(t, Has(Ceiling(AccessFull), Full), "no level hands out the wildcard")

	// A full-access key is narrowed to exactly the ceiling.
	assert.ElementsMatch(t, Ceiling(AccessRead), Intersect([]Scope{Full}, Ceiling(AccessRead)))
	// A write-capable key in a read-only collection keeps only what is allowed.
	got := Intersect([]Scope{ItemsRead, ItemsWrite, ItemsDelete}, Ceiling(AccessRead))
	assert.Equal(t, []Scope{ItemsRead}, got)
	assert.Empty(t, Intersect([]Scope{ItemsRead}, Ceiling(AccessOff)))
}
