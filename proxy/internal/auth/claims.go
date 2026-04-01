// Package auth provides JWT-based authentication for the proxy API.
package auth

import "github.com/golang-jwt/jwt/v5"

// Claims represents the JWT claims used for user access authentication.
type Claims struct {
	jwt.RegisteredClaims
}
