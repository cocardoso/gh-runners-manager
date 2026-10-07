package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/cocardoso/gh-runners-manager/internal/auth"
	"github.com/cocardoso/gh-runners-manager/internal/events"
)

// SessionCookie names the cookie that carries a signed-in browser's session.
const SessionCookie = "ghrm_session"

// ActorToken is the audit actor of requests made with the admin bearer token.
const ActorToken = "token"

type ctxKey int

const (
	actorKey ctxKey = iota
	sessionKey
	requestKey
)

type requestInfo struct {
	secure     bool
	remoteAddr string
	userAgent  string
	cookie     string
}

// Actor returns who made the request: a username, ActorToken, or "" when anonymous.
func Actor(ctx context.Context) string {
	a, _ := ctx.Value(actorKey).(string)
	return a
}

func sessionFrom(ctx context.Context) (auth.Session, bool) {
	s, ok := ctx.Value(sessionKey).(auth.Session)
	return s, ok
}

func requestFrom(ctx context.Context) requestInfo {
	r, _ := ctx.Value(requestKey).(requestInfo)
	return r
}

// public lists the API calls that need no credentials.
func public(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case !strings.HasPrefix(p, "/api/"):
		return true // the UI, /healthz, /readyz, /metrics
	case p == "/api/docs" || strings.HasPrefix(p, "/api/openapi") || strings.HasPrefix(p, "/api/schemas/"):
		return true
	case r.Method == http.MethodGet && p == "/api/v1/auth/session":
		return true
	case r.Method == http.MethodPost && (p == "/api/v1/auth/setup" || p == "/api/v1/auth/login"):
		return true
	}
	return false
}

func problem(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"status": status, "title": http.StatusText(status), "detail": detail})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// authenticate requires a session cookie (plus the CSRF header on writes) or the admin
// bearer token on every API call that is not public (spec §10.6).
func authenticate(d Deps, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := requestInfo{secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
			userAgent: r.UserAgent()}
		info.remoteAddr, _, _ = net.SplitHostPort(r.RemoteAddr)
		if c, err := r.Cookie(SessionCookie); err == nil {
			info.cookie = c.Value
		}
		ctx := context.WithValue(r.Context(), requestKey, info)

		if bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			if d.AdminToken == "" || subtle.ConstantTimeCompare([]byte(bearer), []byte(d.AdminToken)) != 1 {
				problem(w, http.StatusUnauthorized, "invalid admin token")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, actorKey, ActorToken)))
			return
		}
		if info.cookie != "" && d.Auth != nil {
			if sess, user, err := d.Auth.Authenticate(ctx, info.cookie); err == nil {
				ctx = context.WithValue(context.WithValue(ctx, actorKey, user.Username), sessionKey, sess)
				if sess.Refreshed { // the expiry slid: the browser's cookie follows
					c := sessionCookie(ctx, info.cookie, int(d.Auth.TimeLeft(sess).Seconds()))
					http.SetCookie(w, &c)
				}
				if !public(r) && !safeMethod(r.Method) &&
					subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(sess.CSRF)) != 1 {
					problem(w, http.StatusForbidden, "missing or wrong X-CSRF-Token header")
					return
				}
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			} else if !errors.Is(err, auth.ErrNoSession) {
				problem(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		if !public(r) {
			problem(w, http.StatusUnauthorized, "sign in first")
			return
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// audit records an administrative action with its actor (spec §10.6).
func audit(ctx context.Context, d Deps, kind, msg string, refs events.Refs, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["actor"] = Actor(ctx)
	_, _ = d.Recorder.Warn(ctx, "audit."+kind, msg, refs, data)
}

// SessionState is what the UI needs to know about the current visitor.
type SessionState struct {
	State    string `json:"state" enum:"setup,signed_out,signed_in"`
	Username string `json:"username,omitempty"`
	CSRF     string `json:"csrf,omitempty"`
}

func sessionCookie(ctx context.Context, value string, maxAge int) http.Cookie {
	return http.Cookie{Name: SessionCookie, Value: value, Path: "/", MaxAge: maxAge, HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: requestFrom(ctx).secure}
}

func registerAuth(a huma.API, d Deps) {
	tags := []string{"auth"}
	huma.Register(a, huma.Operation{OperationID: "get-session", Method: http.MethodGet, Path: "/api/v1/auth/session",
		Summary: "Who is signed in (public)", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body SessionState }, error) {
			out := &struct{ Body SessionState }{}
			if d.Auth == nil {
				out.Body.State = "signed_out"
				return out, nil
			}
			need, err := d.Auth.NeedsSetup(ctx)
			if err != nil {
				return nil, err
			}
			if sess, ok := sessionFrom(ctx); ok {
				out.Body = SessionState{State: "signed_in", Username: Actor(ctx), CSRF: sess.CSRF}
			} else if need {
				out.Body.State = "setup"
			} else {
				out.Body.State = "signed_out"
			}
			return out, nil
		})

	type cookieOut struct {
		SetCookie http.Cookie `header:"Set-Cookie"`
		Body      SessionState
	}
	type setupIn struct {
		Body struct {
			SetupToken string `json:"setup_token" doc:"The one-time token printed by the installer (data_dir/setup-token)"`
			Username   string `json:"username" minLength:"1" maxLength:"64"`
			Password   string `json:"password" minLength:"1"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "setup", Method: http.MethodPost, Path: "/api/v1/auth/setup",
		Summary: "Create the admin account (first run, public, needs the setup token)", Tags: tags},
		func(ctx context.Context, in *setupIn) (*struct{ Body SessionState }, error) {
			if d.Auth == nil {
				return nil, huma.Error403Forbidden("sign-in is not configured")
			}
			u, err := d.Auth.Setup(ctx, in.Body.SetupToken, in.Body.Username, in.Body.Password)
			switch {
			case errors.Is(err, auth.ErrSetupDone):
				return nil, huma.Error409Conflict(err.Error())
			case errors.Is(err, auth.ErrBadSetupToken):
				return nil, huma.Error403Forbidden(err.Error())
			case errors.Is(err, auth.ErrWeakPassword), errors.Is(err, auth.ErrBadUsername):
				return nil, huma.Error422UnprocessableEntity(err.Error())
			case err != nil:
				return nil, err
			}
			audit(context.WithValue(ctx, actorKey, u.Username), d, "setup", "admin account "+u.Username+" created", events.Refs{}, nil)
			return &struct{ Body SessionState }{Body: SessionState{State: "signed_out"}}, nil
		})

	type loginIn struct {
		Body struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "login", Method: http.MethodPost, Path: "/api/v1/auth/login",
		Summary: "Sign in (public)", Tags: tags},
		func(ctx context.Context, in *loginIn) (*cookieOut, error) {
			if d.Auth == nil {
				return nil, huma.Error403Forbidden("sign-in is not configured")
			}
			req := requestFrom(ctx)
			sess, cookie, err := d.Auth.Login(ctx, in.Body.Username, in.Body.Password, req.remoteAddr, req.userAgent)
			switch {
			case errors.Is(err, auth.ErrBadCredentials):
				audit(ctx, d, "login_failed", "failed sign-in for "+in.Body.Username+" from "+req.remoteAddr, events.Refs{},
					map[string]any{"username": in.Body.Username, "remote_addr": req.remoteAddr})
				return nil, huma.Error401Unauthorized(err.Error())
			case errors.Is(err, auth.ErrThrottled):
				return nil, huma.Error429TooManyRequests(err.Error())
			case err != nil:
				return nil, err
			}
			audit(context.WithValue(ctx, actorKey, in.Body.Username), d, "login", in.Body.Username+" signed in from "+req.remoteAddr, events.Refs{},
				map[string]any{"remote_addr": req.remoteAddr})
			return &cookieOut{SetCookie: sessionCookie(ctx, cookie, int(d.Auth.TimeLeft(sess).Seconds())),
				Body: SessionState{State: "signed_in", Username: in.Body.Username, CSRF: sess.CSRF}}, nil
		})

	huma.Register(a, huma.Operation{OperationID: "logout", Method: http.MethodPost, Path: "/api/v1/auth/logout",
		Summary: "Sign out", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, _ *struct{}) (*struct {
			SetCookie http.Cookie `header:"Set-Cookie"`
		}, error) {
			if d.Auth != nil {
				if err := d.Auth.Logout(ctx, requestFrom(ctx).cookie); err != nil {
					return nil, err
				}
			}
			audit(ctx, d, "logout", Actor(ctx)+" signed out", events.Refs{}, nil)
			return &struct {
				SetCookie http.Cookie `header:"Set-Cookie"`
			}{SetCookie: sessionCookie(ctx, "", -1)}, nil
		})

	type passwordIn struct {
		Body struct {
			Current string `json:"current"`
			Next    string `json:"next"`
		}
	}
	huma.Register(a, huma.Operation{OperationID: "change-password", Method: http.MethodPost, Path: "/api/v1/auth/password",
		Summary: "Change the signed-in user's password; other sessions end", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *passwordIn) (*struct{}, error) {
			sess, ok := sessionFrom(ctx)
			if !ok || d.Auth == nil {
				return nil, huma.Error403Forbidden("sign in with a password to change it")
			}
			err := d.Auth.ChangePassword(ctx, sess, in.Body.Current, in.Body.Next)
			switch {
			case errors.Is(err, auth.ErrBadCredentials):
				return nil, huma.Error403Forbidden("the current password is wrong")
			case errors.Is(err, auth.ErrWeakPassword):
				return nil, huma.Error422UnprocessableEntity(err.Error())
			case err != nil:
				return nil, err
			}
			audit(ctx, d, "password", Actor(ctx)+" changed their password", events.Refs{}, nil)
			return &struct{}{}, nil
		})
}
