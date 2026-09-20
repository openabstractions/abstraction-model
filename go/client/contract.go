package client

// The generated contract types this package's API reaches, re-exported so an
// application names them through this package and never imports the generated
// one. scripts/idiom_check.py refuses a reachable type this file leaves out.

import (
	request "github.com/openabstractions/abstraction-download/go/abstraction/download/request"
	wire "github.com/openabstractions/abstraction-model/go/abstraction/model/api"
)

type Artifact = request.Artifact

type Constraints = request.Constraints

type Network = request.Network

const (
	NetworkAny       = request.NetworkAny
	NetworkUnmetered = request.NetworkUnmetered
)

// NetworkValues returns every member of Network in declaration order, in a new slice.
func NetworkValues() []Network { return request.NetworkValues() }

type Request = request.Request

type Source = request.Source

type LookupOutcome = wire.LookupOutcome

const (
	LookupOutcomeResolved           = wire.LookupOutcomeResolved
	LookupOutcomeInvalid            = wire.LookupOutcomeInvalid
	LookupOutcomeUnavailable        = wire.LookupOutcomeUnavailable
	LookupOutcomeUnsupportedMapping = wire.LookupOutcomeUnsupportedMapping
	LookupOutcomeForbidden          = wire.LookupOutcomeForbidden
)

// LookupOutcomeValues returns every member of LookupOutcome in declaration order, in a new slice.
func LookupOutcomeValues() []LookupOutcome { return wire.LookupOutcomeValues() }

type ServiceError = wire.ServiceError

type ServiceErrorCode = wire.ServiceErrorCode

const (
	ServiceErrorCodeHandlerError   = wire.ServiceErrorCodeHandlerError
	ServiceErrorCodeInvalidResult  = wire.ServiceErrorCodeInvalidResult
	ServiceErrorCodeUnknownVersion = wire.ServiceErrorCodeUnknownVersion
	ServiceErrorCodeUnknownService = wire.ServiceErrorCodeUnknownService
	ServiceErrorCodeUnknownMethod  = wire.ServiceErrorCodeUnknownMethod
	ServiceErrorCodeWrongMode      = wire.ServiceErrorCodeWrongMode
)

// ServiceErrorCodeValues returns every member of ServiceErrorCode in declaration order, in a new slice.
func ServiceErrorCodeValues() []ServiceErrorCode { return wire.ServiceErrorCodeValues() }
