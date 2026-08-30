package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrCanceled is returned when cancel was requested
	ErrCanceled    = fmt.Errorf("provider: %v", context.Canceled)
	ErrUnsupported = errors.New("provider: unsupported")
	ErrNilInitFunc = errors.New("init function can not be nil")
	// ErrNilReplacement is returned by RegisterRemoved when no successor is
	// named. A removed type with nothing to point at is one the
	// unknown-provider error already handles better.
	ErrNilReplacement = errors.New("replacement provider type can not be empty")
)

// ErrRemovedProvider is returned when a config names a provider type this build
// has stopped serving and something else took over from it.
type ErrRemovedProvider struct {
	Name        string
	Replacement string
}

func (err ErrRemovedProvider) Error() string {
	return fmt.Sprintf("provider type %v has been removed; use %v instead", err.Name, err.Replacement)
}

type ErrUnableToConvertFeatureID struct {
	val interface{}
}

func (e ErrUnableToConvertFeatureID) Error() string {
	return fmt.Sprintf("unable to convert feature id %+v to uint64", e.val)
}

// ErrProviderAlreadyExists is returned when the Provider being registered
// already exists in the registration system
type ErrProviderAlreadyExists struct {
	Name string
}

func (err ErrProviderAlreadyExists) Error() string {
	return fmt.Sprintf("provider %s already exists", err.Name)
}

// ErrUnknownProvider is returned when no providers are registered or requested
// provider is not registered
type ErrUnknownProvider struct {
	Name           string
	KnownProviders []string
}

func (err ErrUnknownProvider) Error() string {
	var errStr strings.Builder
	errStr.WriteString("no providers registered")
	if err.Name != "" {
		errStr.Reset()
		fmt.Fprintf(&errStr, "no providers registered by the name %s", err.Name)
	}
	if len(err.KnownProviders) != 0 {
		errStr.WriteString(", known providers:")
		errStr.WriteString(strings.Join(err.KnownProviders, ","))
	}
	return errStr.String()
}

// ErrInvalidProviderType is return when the requested provider type is not known for
// the given name
type ErrInvalidProviderType struct {
	Name           string
	Type           providerType
	KnownProviders []string
}

func (err ErrInvalidProviderType) Error() string {
	var errStr strings.Builder
	fmt.Fprintf(&errStr, "provider '%v' is not of type %v", err.Name, err.Type)
	if len(err.KnownProviders) != 0 {
		fmt.Fprintf(&errStr, ", known providers of type (%v):", err.Type)
		errStr.WriteString(strings.Join(err.KnownProviders, ","))
	}
	return errStr.String()
}

// ErrInvalidRegisteredProvider is returned when something went wrong with the
// provider registration. This should never happen, in normal usage, and if it does it's an issue
// with the provider plugin.
type ErrInvalidRegisteredProvider struct {
	Name string
}

func (err ErrInvalidRegisteredProvider) Error() string {
	return fmt.Sprintf("provider %v did not register correctly, nil init functions registered", err.Name)
}
