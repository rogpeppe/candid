// Copyright 2017 Canonical Ltd.

package httpauth

import (
	"net/http"
	"time"

	"github.com/juju/idmclient/params"
	"github.com/juju/utils"
	"golang.org/x/net/context"
	errgo "gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"

	"github.com/CanonicalLtd/blues-identity/internal/auth"
)

// An Authorizer is used to authorize HTTP requests.
type Authorizer struct {
	authorizer *auth.Authorizer
	oven       *bakery.Oven
}

// New creates a new Authorizer for authorizing HTTP requests made to the
// identity server. The given oven is used to make new macaroons; the
// given authorizer is used as the underlying authorizer.
func New(o *bakery.Oven, a *auth.Authorizer) *Authorizer {
	return &Authorizer{
		authorizer: a,
		oven:       o,
	}
}

// Auth checks that client making the given request is authorized to
// perform the given operations. It may return an httpbakery error when
// further checks are required, or params.ErrUnauthorized if the user is
// authenticated but does not have the required authorization.
func (a *Authorizer) Auth(ctx context.Context, req *http.Request, ops ...bakery.Op) (*bakery.AuthInfo, error) {
	ctx = httpbakery.ContextWithRequest(ctx, req)
	if username, password, ok := req.BasicAuth(); ok {
		ctx = auth.ContextWithUserCredentials(ctx, username, password)
	}
	authInfo, err := a.authorizer.Auth(ctx, httpbakery.RequestMacaroons(req), ops...)
	if err == nil {
		return authInfo, nil
	}
	derr, ok := errgo.Cause(err).(*bakery.DischargeRequiredError)
	if !ok {
		return nil, errgo.Mask(err, errgo.Is(params.ErrUnauthorized))
	}
	m, err := a.oven.NewMacaroon(
		ctx,
		httpbakery.RequestVersion(req),
		time.Now().Add(365*24*time.Hour),
		derr.Caveats,
		derr.Ops...,
	)
	if err != nil {
		return nil, errgo.Notef(err, "cannot create macaroon")
	}
	mpath, err := utils.RelativeURLPath(req.URL.Path, "/")
	if err != nil {
		return nil, errgo.Mask(err)
	}
	err = httpbakery.NewDischargeRequiredErrorForRequest(m, mpath, derr, req)
	err.(*httpbakery.Error).Info.CookieNameSuffix = "idm"
	return nil, err
}