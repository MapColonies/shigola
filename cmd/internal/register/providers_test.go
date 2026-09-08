package register_test

import (
	"testing"

	"github.com/MapColonies/shigola/cmd/internal/register"
	"github.com/MapColonies/shigola/dict"
	_ "github.com/MapColonies/shigola/provider/test"
)

func TestProviders(t *testing.T) {
	type tcase struct {
		config      []dict.Dict
		expectedErr error
	}

	fn := func(tc tcase) func(*testing.T) {
		return func(t *testing.T) {
			var err error

			// convert []dict.Dict -> []dict.Dicter
			provArr := make([]dict.Dicter, len(tc.config))
			for i := range provArr {
				provArr[i] = tc.config[i]
			}

			_, err = register.Providers(provArr, nil)
			if tc.expectedErr != nil {
				if err.Error() != tc.expectedErr.Error() {
					t.Errorf("invalid error. expected: %v, got %v", tc.expectedErr, err.Error())
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected err: %v", err)
				return
			}
		}
	}

	tests := map[string]tcase{
		"missing name": {
			config: []dict.Dict{
				{
					"type": "mvt_postgis",
				},
			},
			expectedErr: register.ErrProviderNameMissing,
		},
		"name is not string": {
			config: []dict.Dict{
				{
					"name": 1,
				},
			},
			expectedErr: register.ErrProviderNameInvalid,
		},
		"missing type": {
			config: []dict.Dict{
				{
					"name": "test",
				},
			},
			expectedErr: register.ErrProviderTypeMissing("test"),
		},
		"invalid type": {
			config: []dict.Dict{
				{
					"name": "test",
					"type": 1,
				},
			},
			expectedErr: register.ErrProviderTypeInvalid("test"),
		},
		"already registered": {
			config: []dict.Dict{
				{
					"name": "test",
					"type": "mvt_test",
				},
				{
					"name": "test",
					"type": "mvt_test",
				},
			},
			expectedErr: register.ErrProviderAlreadyRegistered("test"),
		},
		"success": {
			config: []dict.Dict{
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
