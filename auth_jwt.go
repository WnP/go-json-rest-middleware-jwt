package jwt

import (
	"github.com/ant0ine/go-json-rest/rest"
	"github.com/dgrijalva/jwt-go"

	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// JWTMiddleware provides a Json-Web-Token authentication implementation. On failure, a 401 HTTP response
// is returned. On success, the wrapped middleware is called, and the userId is made available as
// request.Env["REMOTE_USER"].(string).
// Users can get a token by posting a json request to LoginHandler. The token then needs to be passed in
// the Authentication header. Example: Authorization:Bearer XXX_TOKEN_XXX
type JWTMiddleware struct {
	// Realm name to display to the user. Required.
	Realm string

	// signing algorithm - possible values are HS256, HS384, HS512
	// Optional, default is HS256.
	SigningAlgorithm string

	// Secret key used for signing. Required.
	Key []byte

	// Duration that a jwt token is valid. Optional, defaults to one hour.
	Timeout time.Duration

	// This field allows clients to refresh their token until MaxRefresh has passed.
	// Note that clients can refresh their token in the last moment of MaxRefresh.
	// This means that the maximum validity timespan for a token is MaxRefresh + Timeout.
	// Optional, defaults to 0 meaning not refreshable.
	MaxRefresh time.Duration

	// Callback function that should perform the authentication of the user based on userId and
	// password. Must return true on success, false on failure. Required.
	Authenticator func(userId string, password string) bool

	// Callback function that should perform the authorization of the authenticated user. Called
	// only after an authentication success. Must return true on success, false on failure.
	// Optional, default to success.
	Authorizator func(userId string, request *rest.Request) bool
	
	// need prompt return on unauthorized
	NeedPrompt bool
}

// MiddlewareFunc makes JWTMiddleware implement the Middleware interface.
func (mw *JWTMiddleware) MiddlewareFunc(handler rest.HandlerFunc) rest.HandlerFunc {

	if mw.Realm == "" {
		log.Fatal("Realm is required")
	}
	if mw.SigningAlgorithm == "" {
		mw.SigningAlgorithm = "HS256"
	}
	if mw.Key == nil {
		log.Fatal("Key required")
	}
	if mw.Timeout == 0 {
		mw.Timeout = time.Hour
	}
	if mw.Authenticator == nil {
		log.Fatal("Authenticator is required")
	}
	if mw.Authorizator == nil {
		mw.Authorizator = func(userId string, request *rest.Request) bool {
			return true
		}
	}

	return func(writer rest.ResponseWriter, request *rest.Request) { mw.middlewareImpl(writer, request, handler) }
}

func (mw *JWTMiddleware) middlewareImpl(writer rest.ResponseWriter, request *rest.Request, handler rest.HandlerFunc) {
	token, err := parseToken(request, mw.Key)

	if err != nil {
		mw.unauthorized(writer)
		return
	}

	id := token.Claims["id"].(string)

	if !mw.Authorizator(id, request) {
		mw.unauthorized(writer)
		return
	}

	request.Env["REMOTE_USER"] = id
	handler(writer, request)
}

type login struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Handler that clients can use to get a jwt token.
// Payload needs to be json in the form of {"username": "USERNAME", "password": "PASSWORD"}.
// Reply will be of the form {"token": "TOKEN"}.
func (mw *JWTMiddleware) LoginHandler(writer rest.ResponseWriter, request *rest.Request) {
	login_vals := login{}
	err := request.DecodeJsonPayload(&login_vals)

	if err != nil {
		mw.unauthorized(writer)
		return
	}

	if !mw.Authenticator(login_vals.Username, login_vals.Password) {
		mw.unauthorized(writer)
		return
	}

	token := jwt.New(jwt.GetSigningMethod(mw.SigningAlgorithm))
	token.Claims["id"] = login_vals.Username
	token.Claims["exp"] = time.Now().Add(mw.Timeout).Unix()
	if mw.MaxRefresh != 0 {
		token.Claims["orig_iat"] = time.Now().Unix()
	}
	tokenString, err := token.SignedString(mw.Key)

	if err != nil {
		mw.unauthorized(writer)
		return
	}

	writer.WriteJson(&map[string]string{"token": tokenString})
}

func parseToken(request *rest.Request, key []byte) (*jwt.Token, error) {
	authHeader := request.Header.Get("Authorization")

	if authHeader == "" {
		return nil, errors.New("Auth header empty")
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if !(len(parts) == 2 && parts[0] == "Bearer") {
		return nil, errors.New("Invalid auth header")
	}

	return jwt.Parse(parts[1], func(token *jwt.Token) (interface{}, error) {
		return key, nil
	})
}

type token struct {
	Token string `json:"token"`
}

// Handler that clients can use to refresh their token. The token still needs to be valid on refresh.
// Shall be put under an endpoint that is using the JWTMiddleware.
// Reply will be of the form {"token": "TOKEN"}.
func (mw *JWTMiddleware) RefreshHandler(writer rest.ResponseWriter, request *rest.Request) {
	token, err := parseToken(request, mw.Key)

	// Token should be valid anyway as the RefreshHandler is authed
	if err != nil {
		mw.unauthorized(writer)
		return
	}

	origIat := int64(token.Claims["orig_iat"].(float64))

	if origIat < time.Now().Add(-mw.MaxRefresh).Unix() {
		mw.unauthorized(writer)
		return
	}

	newToken := jwt.New(jwt.GetSigningMethod(mw.SigningAlgorithm))
	newToken.Claims["id"] = token.Claims["id"]
	newToken.Claims["exp"] = time.Now().Add(mw.Timeout).Unix()
	newToken.Claims["orig_iat"] = origIat
	tokenString, err := newToken.SignedString(mw.Key)

	if err != nil {
		mw.unauthorized(writer)
		return
	}

	writer.WriteJson(&map[string]string{"token": tokenString})
}

func (mw *JWTMiddleware) unauthorized(writer rest.ResponseWriter) {
	if mw.NeedPrompt {
		writer.Header().Set("WWW-Authenticate", "Basic realm="+mw.Realm)
		rest.Error(writer, "Not Authorized", http.StatusUnauthorized)
	} else {
		writer.WriteJson(&map[string]string{"Error": "Not Authorized"})
	}
}
