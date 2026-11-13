//go:build !linux && !darwin

package management

import "errors"

func PolicyLease(string) (func(), error) { return nil, errors.New("management_platform_not_supported") }
func WritePolicy(string, Policy) error   { return errors.New("management_platform_not_supported") }
