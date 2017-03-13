// Copyright 2014 Canonical Ltd.

package store

import (
	"bytes"
	"net/http"
	"strings"
	"time"

	"github.com/juju/idmclient/params"
	"github.com/juju/utils"
	"golang.org/x/net/context"
	"gopkg.in/errgo.v1"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery"
	"gopkg.in/macaroon-bakery.v2-unstable/bakery/checkers"
	"gopkg.in/macaroon-bakery.v2-unstable/httpbakery"
)

const (
	AdminUsername     = "admin@idm"
	SSHKeyGetterGroup = "sshkeygetter@idm"
	GroupListGroup    = "grouplist@idm"
)

const (
	kindGlobal = "global"
	kindUser   = "u"
)

// The following constants define possible operation actions.
const (
	ActionRead               = "read"
	ActionVerify             = "verify"
	ActionDischargeFor       = "dischargeFor"
	ActionCreateAgent        = "createAgent"
	ActionReadAdmin          = "readAdmin"
	ActionWriteAdmin         = "writeAdmin"
	ActionReadGroups         = "readGroups"
	ActionWriteGroups        = "writeGroups"
	ActionReadSSHKeys        = "readSSHKeys"
	ActionWriteSSHKeys       = "writeSSHKeys"
	ActionLogin              = "login"
	ActionReadDischargeToken = "read-discharge-token"
)

// TODO(mhilton) make the admin ACL configurable
var AdminACL = []string{AdminUsername}

func (s *Store) aclForOp(op bakery.Op) ([]string, error) {
	kind, name := splitEntity(op.Entity)
	switch kind {
	case kindGlobal:
		if name != "" {
			return nil, nil
		}
		switch op.Action {
		case ActionRead:
			// Only admins are allowed to read global information.
			return AdminACL, nil
		case ActionDischargeFor:
			// Only admins are allowed to discharge for other users.
			return AdminACL, nil
		case ActionVerify:
			// Everyone is allowed to verify a macaroon.
			return []string{bakery.Everyone}, nil
		case ActionLogin:
			// Everyone is allowed to log in.
			return []string{bakery.Everyone}, nil
		}
	case kindUser:
		if name == "" {
			return nil, nil
		}
		username := name
		acl := make([]string, 0, len(AdminACL)+2)
		acl = append(acl, AdminACL...)
		switch op.Action {
		case ActionRead:
			return append(acl, username), nil
		case ActionCreateAgent:
			return append(acl, "+create-agent@"+username), nil
		case ActionReadAdmin:
			return acl, nil
		case ActionWriteAdmin:
			return acl, nil
		case ActionReadGroups:
			// Administrators, users with GroupList permissions and the user
			// themselves can list their groups.
			return append(acl, username, GroupListGroup), nil
		case ActionWriteGroups:
			// Only administrators can set a user's groups.
			return acl, nil
		case ActionReadSSHKeys:
			return append(acl, username, SSHKeyGetterGroup), nil
		case ActionWriteSSHKeys:
			return append(acl, username), nil
		}
	}
	logger.Infof("no ACL found for op %#v", op)
	return nil, nil
}

// checkAdminCredentials checks if the request has credentials that match the
// configured administration credentials for the server. If the credentials match
// nil will be reurned, otherwise the error will describe the failure.
//
// If there are no credentials in the request, it returns params.ErrNoAdminCredsProvided.
func (s *Store) checkAdminCredentials(req *http.Request) error {
	if _, ok := req.Header["Authorization"]; !ok {
		return params.ErrNoAdminCredsProvided
	}
	u, p, err := utils.ParseBasicAuthHeader(req.Header)
	if err != nil {
		return errgo.WithCausef(err, params.ErrUnauthorized, "")
	}
	if u != s.pool.params.AuthUsername {
		return errgo.WithCausef(nil, params.ErrUnauthorized, "invalid credentials")
	}
	if p != s.pool.params.AuthPassword {
		return errgo.WithCausef(nil, params.ErrUnauthorized, "invalid credentials")
	}
	return nil
}

// UserHasPublicKeyCaveat creates a first-party caveat that ensures that
// the given user is associated with the given public key.
func UserHasPublicKeyCaveat(user params.Username, pk *bakery.PublicKey) checkers.Caveat {
	return checkers.Caveat{
		Namespace: checkersNamespace,
		Condition: checkers.Condition(userHasPublicKeyCondition, string(user)+" "+pk.String()),
	}
}

type storeKey struct{}

func storeFromContext(ctx context.Context) *Store {
	store, _ := ctx.Value(storeKey{}).(*Store)
	return store
}

func ContextWithStore(ctx context.Context, store *Store) context.Context {
	return context.WithValue(ctx, storeKey{}, store)
}

type requestKey struct{}

func requestFromContext(ctx context.Context) *http.Request {
	req, _ := ctx.Value(requestKey{}).(*http.Request)
	return req
}

func contextWithRequest(ctx context.Context, req *http.Request) context.Context {
	return context.WithValue(ctx, requestKey{}, req)
}

const checkersNamespace = "jujucharms.com/identity"
const userHasPublicKeyCondition = "user-has-public-key"

func newChecker() *checkers.Checker {
	checker := httpbakery.NewChecker()
	checker.Namespace().Register(checkersNamespace, "")
	checker.Register(userHasPublicKeyCondition, checkersNamespace, checkUserHasPublicKey)
	return checker
}

// checkUserHasPublicKey checks the "user-has-public-key" caveat.
func checkUserHasPublicKey(ctxt context.Context, cond, arg string) error {
	store := storeFromContext(ctxt)
	if store == nil {
		return errgo.Newf("no store in context")
	}
	parts := strings.Fields(arg)
	if len(parts) != 2 {
		return errgo.New("caveat badly formatted")
	}
	var username params.Username
	err := username.UnmarshalText([]byte(parts[0]))
	if err != nil {
		return errgo.Mask(err)
	}
	var publicKey bakery.PublicKey
	err = publicKey.UnmarshalText([]byte(parts[1]))
	if err != nil {
		return errgo.Notef(err, "invalid public key %q", parts[1])
	}
	id, err := store.GetIdentity(username)
	if err != nil {
		if errgo.Cause(err) != params.ErrNotFound {
			return errgo.Mask(err)
		}
		return errgo.Newf("public key not valid for user")
	}
	for _, pk := range id.PublicKeys {
		if !bytes.Equal(pk.Key, publicKey.Key[:]) {
			continue
		}
		return nil
	}
	return errgo.Newf("public key not valid for user")
}

// Authorize checks that client making the given request is authorized
// to perform the given operations. It may return an httpbakery error
// when further checks are required, or params.ErrUnauthorized
// if the user is authenticated but does not have the required
// authorization.
func (s *Store) Authorize(ctx context.Context, req *http.Request, ops ...bakery.Op) (*bakery.AuthInfo, error) {
	ctx = ContextWithStore(ctx, s)
	// TODO putting two requests in the same context feels a bit wrong. Perhaps
	// they should be consolidated somehow.
	ctx = contextWithRequest(ctx, req)
	ctx = httpbakery.ContextWithRequest(ctx, req)
	authInfo, err := s.Bakery.Checker.Auth(httpbakery.RequestMacaroons(req)...).Allow(ctx, ops...)
	if err != nil {
		return nil, s.maybeDischargeRequiredError(ctx, req, err)
	}
	return authInfo, nil
}

func (s *Store) maybeDischargeRequiredError(ctx context.Context, req *http.Request, checkErr error) error {
	derr, ok := errgo.Cause(checkErr).(*bakery.DischargeRequiredError)
	if !ok {
		if errgo.Cause(checkErr) == bakery.ErrPermissionDenied {
			checkErr = errgo.WithCausef(checkErr, params.ErrUnauthorized, "")
		}
		return errgo.Mask(checkErr, errgo.Any)
	}
	m, err := s.Bakery.Oven.NewMacaroon(
		ctx,
		httpbakery.RequestVersion(req),
		time.Now().Add(365*24*time.Hour),
		derr.Caveats,
		derr.Ops...,
	)
	if err != nil {
		return errgo.Notef(err, "cannot create macaroon")
	}
	mpath, err := utils.RelativeURLPath(req.URL.Path, "/")
	if err != nil {
		return errgo.Mask(err)
	}
	err = httpbakery.NewDischargeRequiredErrorForRequest(m, mpath, checkErr, req)
	err.(*httpbakery.Error).Info.CookieNameSuffix = "idm"
	return err
}

type identityClient struct {
	location string
}

func (c identityClient) IdentityFromContext(ctx context.Context) (_ident bakery.Identity, _ []checkers.Caveat, _ error) {
	logger.Debugf("identity from context %v {", ctx)
	defer func() {
		logger.Debugf("} -> ident %#v", _ident)
	}()
	req, store := requestFromContext(ctx), storeFromContext(ctx)
	if store == nil {
		return nil, nil, errgo.Newf("no store found in context")
	}
	if req != nil {
		err := store.checkAdminCredentials(req)
		if err == nil {
			logger.Debugf("admin login success as %q", AdminUsername)
			return Identity(AdminUsername), nil, nil
		}
		if errgo.Cause(err) != params.ErrNoAdminCredsProvided {
			logger.Debugf("admin login failed for some reason: %v", err)
			return nil, nil, errgo.Mask(err, errgo.Is(params.ErrUnauthorized))
		}
		logger.Debugf("admin login failed - no admin creds provided")
	}
	return nil, []checkers.Caveat{
		checkers.NeedDeclaredCaveat(
			checkers.Caveat{
				Location:  store.pool.params.Location,
				Condition: "is-authenticated-user",
			},
			"username",
		),
	}, nil
}

type requiredDomainKey struct{}

// ContextWithRequiredDomain returns a context associated
// with the given domain, such that declared identities
// will only be allowed if they have that domain.
func ContextWithRequiredDomain(ctx context.Context, domain string) context.Context {
	return context.WithValue(ctx, requiredDomainKey{}, domain)
}

// CheckUserDomain checks that the given user name has
// a valid domain name with respect to the given context
// (see also ContextWithRequiredDomain).
func CheckUserDomain(ctx context.Context, username string) error {
	domain, ok := ctx.Value(requiredDomainKey{}).(string)
	if ok && !strings.HasSuffix(username, "@"+domain) {
		return errgo.Newf("%q not in required domain %q", username, domain)
	}
	return nil
}

func (c identityClient) DeclaredIdentity(ctx context.Context, declared map[string]string) (bakery.Identity, error) {
	username, ok := declared["username"]
	if !ok {
		return nil, errgo.Newf("no declared user")
	}
	if err := CheckUserDomain(ctx, username); err != nil {
		return nil, errgo.Mask(err)
	}
	return Identity(username), nil
}

type Identity string

func (id Identity) Id() string {
	return string(id)
}

func (id Identity) Domain() string {
	return ""
}

// Allow implements bakery.ACLIdentity.Allow by checking whether the
// given identity is in any of the required groups or users.
// It uses the store associated with the context (see ContextWithStore)
// to retrieve the groups.
func (id Identity) Allow(ctx context.Context, acl []string) (bool, error) {
	logger.Debugf("Identity.Allow %q, acl %q {", id, acl)
	defer logger.Debugf("}")
	if ok, isTrivial := trivialAllow(string(id), acl); isTrivial {
		logger.Debugf("trivial %v", ok)
		return ok, nil
	}
	groups, err := id.Groups(ctx)
	if err != nil {
		logger.Debugf("error getting groups: %v", err)
		return false, errgo.Mask(err)
	}
	for _, a := range acl {
		for _, g := range groups {
			if g == a {
				logger.Infof("success (group %q)", g)
				return true, nil
			}
		}
	}
	logger.Debugf("not in groups")
	return false, nil
}

// Groups returns all the groups associated with the user.
// It uses the store associated with the context (see ContextWithStore)
// to retrieve the groups.
func (id Identity) Groups(ctx context.Context) ([]string, error) {
	store := storeFromContext(ctx)
	if store == nil {
		return nil, errgo.New("no store found in context")
	}
	idDoc, err := store.GetIdentity(params.Username(id))
	if err != nil {
		return nil, errgo.Mask(err, errgo.Is(params.ErrNotFound))
	}
	var groups []string
	switch {
	case idDoc.ExternalID != "":
		var lpGroups []string
		if getter := store.pool.params.ExternalGroupGetter; getter != nil {
			lpGroups, err = getter.GetGroups(idDoc.ExternalID)
			if err != nil {
				logger.Errorf("Failed to get launchpad groups for user: %s", err)
			}
		}
		groups = uniqueStrings(append(idDoc.Groups, lpGroups...))
	case idDoc.Owner != "":
		groups = filterGroups(ctx, idDoc.Groups, idDoc.Owner)
	}
	return groups, nil
}

// filterGroups removes any entry in groups that is not in the owner's groups set.
func filterGroups(ctx context.Context, groups []string, owner string) []string {
	if owner == AdminUsername {
		// Admin is in every group by definition.
		return groups
	}
	ownerGroups, err := Identity(owner).Groups(ctx)
	if err != nil {
		logger.Errorf("cannot get owner group information: %s", err)
		return nil
	}
	filtered := make([]string, 0, len(groups))
	for _, g := range groups {
		for _, g1 := range ownerGroups {
			if g == g1 {
				filtered = append(filtered, g)
			}
		}
	}
	return filtered
}

// trivialAllow reports whether the username should be allowed
// access to the given ACL based on a superficial inspection
// of the ACL. If there is a definite answer, it will return
// a true isTrivial; otherwise it will return (false, false).
func trivialAllow(username string, acl []string) (allow, isTrivial bool) {
	if len(acl) == 0 {
		return false, true
	}
	for _, name := range acl {
		if name == "everyone" || name == username {
			return true, true
		}
	}
	return false, false
}

func UserOp(u params.Username, action string) bakery.Op {
	return op(kindUser+"-"+string(u), action)
}

func GlobalOp(action string) bakery.Op {
	return op(kindGlobal, action)
}

func op(entity, action string) bakery.Op {
	return bakery.Op{
		Entity: entity,
		Action: action,
	}
}

func splitEntity(entity string) (string, string) {
	if i := strings.Index(entity, "-"); i > 0 {
		return entity[0:i], entity[i+1:]
	}
	return entity, ""
}
