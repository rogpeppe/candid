// Copyright 2014 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package identity

import (
	"html/template"
	"net/http"
	"sort"
	"time"

	"github.com/juju/utils/debugstatus"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v2/bakery"

	"github.com/CanonicalLtd/blues-identity/idp"
	"github.com/CanonicalLtd/blues-identity/idp/agent"
	"github.com/CanonicalLtd/blues-identity/internal/debug"
	"github.com/CanonicalLtd/blues-identity/internal/discharger"
	"github.com/CanonicalLtd/blues-identity/internal/identity"
	"github.com/CanonicalLtd/blues-identity/internal/v1"
	"github.com/CanonicalLtd/blues-identity/meeting"
	"github.com/CanonicalLtd/blues-identity/store"
)

// Versions of the API that can be served.
const (
	Debug      = "debug"
	Discharger = "discharger"
	V1         = "v1"
)

var versions = map[string]identity.NewAPIHandlerFunc{
	Debug:      debug.NewAPIHandler,
	Discharger: discharger.NewAPIHandler,
	V1:         v1.NewAPIHandler,
}

// Versions returns all known API version strings in alphabetical order.
func Versions() []string {
	vs := make([]string, 0, len(versions))
	for v := range versions {
		vs = append(vs, v)
	}
	sort.Strings(vs)
	return vs
}

// ServerParams contains configuration parameters for a server.
type ServerParams struct {
	// MeetingStore holds the storage that will be used to store
	// rendezvous information.
	MeetingStore meeting.Store

	// ProviderDataStore holds the storeage that can be used by
	// identity providers to store data that is not associated with
	// an individual identity.
	ProviderDataStore store.ProviderDataStore

	// RootKeyStore holds the root key store that will be used to
	// store macaroon root keys within the identity server.
	RootKeyStore bakery.RootKeyStore

	// Store holds the identities store for the identity server.
	Store store.Store

	// AuthUsername holds the username for admin login.
	AuthUsername string

	// AuthPassword holds the password for admin login.
	AuthPassword string

	// Key holds the keypair to use with the bakery service.
	Key *bakery.KeyPair

	// Location holds a URL representing the externally accessible
	// base URL of the service, without a trailing slash.
	Location string

	// PrivateAddr should hold a dialable address that will be used
	// for communication between identity servers. Note that this
	// should not contain a port.
	PrivateAddr string

	// IdentityProviders contains the set of identity providers that
	// should be initialised by the service.
	IdentityProviders []idp.IdentityProvider

	// DebugTeams contains the set of launchpad teams that may access
	// the restricted debug endpoints.
	DebugTeams []string

	// AdminAgentPublicKey contains the public key of the admin agent.
	AdminAgentPublicKey *bakery.PublicKey

	// StaticFileSystem contains an http.FileSystem that can be used
	// to serve static files.
	StaticFileSystem http.FileSystem

	// Template contains a set of templates that are used to generate
	// html output.
	Template *template.Template

	// DebugStatusCheckerFuncs contains functions that will be
	// executed as part of a /debug/status check.
	DebugStatusCheckerFuncs []debugstatus.CheckerFunc

	// WaitTimeout holds the time after which an interactive discharge wait
	// request will timeout.
	WaitTimeout time.Duration
}

// NewServer returns a new handler that handles identity service requests and
// stores its data in the given database. The handler will serve the specified
// versions of the API.
func NewServer(params ServerParams, serveVersions ...string) (HandlerCloser, error) {
	// Remove the agent identity provider if it is specified as it is no longer used.
	idps := make([]idp.IdentityProvider, 0, len(params.IdentityProviders))
	for _, idp := range params.IdentityProviders {
		if idp == agent.IdentityProvider {
			continue
		}
		idps = append(idps, idp)
	}
	params.IdentityProviders = idps
	newAPIs := make(map[string]identity.NewAPIHandlerFunc)
	for _, vers := range serveVersions {
		newAPI := versions[vers]
		if newAPI == nil {
			return nil, errgo.Newf("unknown version %q", vers)
		}
		newAPIs[vers] = newAPI
	}
	return identity.New(identity.ServerParams(params), newAPIs)
}

type HandlerCloser interface {
	http.Handler
	Close()
}
