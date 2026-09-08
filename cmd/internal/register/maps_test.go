package register_test

import (
	"errors"
	"testing"

	"github.com/MapColonies/shigola/atlas"
	"github.com/MapColonies/shigola/cmd/internal/register"
	"github.com/MapColonies/shigola/dict"
	"github.com/MapColonies/shigola/internal/env"
	"github.com/MapColonies/shigola/provider"
	_ "github.com/MapColonies/shigola/provider/test"
)

func TestMaps(t *testing.T) {
	type tcase struct {
		atlas       atlas.Atlas
		maps        []provider.Map
		providers   []dict.Dict
		expectedErr error
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			var err error

			// convert []dict.Dict -> []dict.Dicter
			provArr := make([]dict.Dicter, len(tc.providers))
			for i := range provArr {
				provArr[i] = tc.providers[i]
			}

			providers, err := register.Providers(provArr, tc.maps)
			if err != nil {
				t.Errorf("unexpected err: %v", err)
				return
			}

			err = register.Maps(&tc.atlas, tc.maps, providers)
			if !errors.Is(err, tc.expectedErr) {
				t.Errorf("invalid error, expected %v got %v", tc.expectedErr, err)
			}
			return
		}
	}

	tests := map[string]tcase{
		"provider layer invalid": {
			maps: []provider.Map{
				{
					Name: "foo",
					Layers: []provider.MapLayer{
						{
							ProviderLayer: "bar",
						},
					},
				},
			},
			providers: []dict.Dict{
				{
					"name": "test",
					"type": "mvt_test",
				},
			},
			expectedErr: register.ErrProviderLayerInvalid{
				ProviderLayer: "bar",
				Map:           "foo",
			},
		},
		"provider not found": {
			maps: []provider.Map{
				{
					Name: "foo",
					Layers: []provider.MapLayer{
						{
							ProviderLayer: "bar.baz",
						},
					},
				},
			},
			expectedErr: register.ErrProviderNotFound{
				Provider: "bar",
			},
		},
		"provider layer not registered with provider": {
			maps: []provider.Map{
				{
					Name: "foo",
					Layers: []provider.MapLayer{
						{
							ProviderLayer: "test.bar",
						},
					},
				},
			},
			providers: []dict.Dict{
				{
					"name": "test",
					"type": "mvt_test",
				},
			},
			expectedErr: register.ErrProviderLayerNotRegistered{
				MapName:       "foo",
				ProviderLayer: "test.bar",
				Provider:      "test",
			},
		},
		"a map with a layer": {
			maps: []provider.Map{
				{
					Name: "foo",
					Layers: []provider.MapLayer{
						{
							ProviderLayer: "test.test-layer",
						},
					},
				},
			},
			providers: []dict.Dict{
				{
					"name": "test",
					"type": "mvt_test",
				},
			},
		},
		"success": {
			maps: []provider.Map{},
			providers: []dict.Dict{
				{
					"name": "test",
					"type": "mvt_test",
				},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

func TestSanitizeAttribution(t *testing.T) {
	type tcase struct {
		input    string
		expected string
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			result := register.SanitizeAttribution(tc.input)
			if result != tc.expected {
				t.Errorf("expected %v got %v", tc.expected, result)
			}
		}
	}

	tests := map[string]tcase{
		"plain text": {
			input:    `foo`,
			expected: `foo`,
		},
		"HTML must escaped": {
			input:    `<script>true</script>`,
			expected: `&lt;script&gt;true&lt;/script&gt;`,
		},
		"link must not escaped": {
			input:    `<a href="http://example.com">foo</a>`,
			expected: `<a href="http://example.com">foo</a>`,
		},
		"2 links": {
			input:    `foo <a href="http://example.com">bar</a> - <a href="http://example.com" target="_blank">zoo</a>`,
			expected: `foo <a href="http://example.com">bar</a> - <a href="http://example.com" target="_blank">zoo</a>`,
		},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestMapsServeLayerCollections covers the config-to-atlas hop for the flag:
// an omitted key and an explicit true both leave the layer tier published, and
// only an explicit false takes it away (MAPCO-11493).
func TestMapsServeLayerCollections(t *testing.T) {
	type tcase struct {
		configured *env.Bool
		expected   bool
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			maps := []provider.Map{
				{
					Name:                  "osm",
					ServeLayerCollections: tc.configured,
					Layers: []provider.MapLayer{
						{ProviderLayer: "test.test-layer"},
					},
				},
			}

			providers, err := register.Providers([]dict.Dicter{
				dict.Dict{"name": "test", "type": "mvt_test"},
			}, maps)
			if err != nil {
				t.Fatalf("Providers() = %v, want nil", err)
			}

			var a atlas.Atlas
			if err := register.Maps(&a, maps, providers); err != nil {
				t.Fatalf("Maps() = %v, want nil", err)
			}

			m, err := a.Map("osm")
			if err != nil {
				t.Fatalf("Map() = %v, want nil", err)
			}

			if got := m.ServesLayerCollections(); got != tc.expected {
				t.Errorf("ServesLayerCollections() = %v, want %v", got, tc.expected)
			}
		}
	}

	tests := map[string]tcase{
		"omitted":        {configured: nil, expected: true},
		"explicit true":  {configured: env.BoolPtr(true), expected: true},
		"explicit false": {configured: env.BoolPtr(false), expected: false},
	}

	for name, tc := range tests {
		t.Run(name, fn(tc))
	}
}

// TestMapsServeLayerCollectionsCoexist covers one config holding both kinds of
// map: the flag is per map, so a map that declines the layer tier must not
// decide anything for the map next to it (MAPCO-11493).
func TestMapsServeLayerCollectionsCoexist(t *testing.T) {
	maps := []provider.Map{
		{
			Name:                  "whole",
			ServeLayerCollections: env.BoolPtr(false),
			Layers: []provider.MapLayer{
				{ProviderLayer: "test.test-layer"},
			},
		},
		{
			Name: "osm",
			Layers: []provider.MapLayer{
				{ProviderLayer: "test.test-layer"},
			},
		},
	}

	providers, err := register.Providers([]dict.Dicter{
		dict.Dict{"name": "test", "type": "mvt_test"},
	}, maps)
	if err != nil {
		t.Fatalf("Providers() = %v, want nil", err)
	}

	var a atlas.Atlas
	if err := register.Maps(&a, maps, providers); err != nil {
		t.Fatalf("Maps() = %v, want nil", err)
	}

	expected := map[string]bool{"whole": false, "osm": true}
	for name, want := range expected {
		m, err := a.Map(name)
		if err != nil {
			t.Fatalf("Map(%q) = %v, want nil", name, err)
		}

		if got := m.ServesLayerCollections(); got != want {
			t.Errorf("%v ServesLayerCollections() = %v, want %v", name, got, want)
		}
	}
}
