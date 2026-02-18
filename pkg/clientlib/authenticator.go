// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package clientlib

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"velda.io/velda/pkg/proto"
)

var RefreshTokenExpiredError = errors.New("refresh token is expired, please re-login")
var NotLoginnedError = errors.New("Not login yet. Login with `velda auth login`")

const refreshTokenDb = "/refresh_token.db"
const accessTokenDb = "/access_token.db"
const defaultExpirationBuffer = time.Minute

type Authenticator struct {
	ExpirationBuffer       time.Duration
	Profile                string
	dbPath                 string
	refreshToken           string
	refreshTokenExpiration time.Time
	accessToken            string
	accessTokenExpiration  time.Time
	authClient             proto.AuthServiceClient
	mu                     sync.Mutex
}

func NewAuthenticator(profile string, storagePath string, authClient proto.AuthServiceClient) (*Authenticator, error) {
	return &Authenticator{
		Profile:          profile,
		dbPath:           storagePath,
		authClient:       authClient,
		ExpirationBuffer: defaultExpirationBuffer,
	}, nil
}

func (a *Authenticator) GetAuthClient() proto.AuthServiceClient {
	return a.authClient
}

func (a *Authenticator) Login(ctx context.Context, resp *proto.LoginResponse) error {
	err := saveToken(a.dbPath+refreshTokenDb, resp.RefreshToken, a.Profile, resp.RefreshTokenExpiresAt.AsTime())
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refreshToken = resp.RefreshToken
	a.refreshTokenExpiration = resp.RefreshTokenExpiresAt.AsTime()
	return nil
}

func (a *Authenticator) GetRefreshToken(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.getRefreshTokenLocked(ctx)
}

func (a *Authenticator) getRefreshTokenLocked(ctx context.Context) (string, error) {
	if a.refreshToken != "" && a.refreshTokenExpiration.After(time.Now()) {
		return a.refreshToken, nil
	}
	token, expiration, err := loadToken(a.dbPath+refreshTokenDb, a.Profile)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", NotLoginnedError
		}
		return "", fmt.Errorf("failed to load refresh token: %w", err)
	}
	if expiration.Before(time.Now()) {
		return "", RefreshTokenExpiredError
	}
	a.refreshToken = token
	a.refreshTokenExpiration = expiration
	return token, nil
}

func (a *Authenticator) GetAccessToken(ctx context.Context) (string, error) {
	if IsInSession() {
		return a.getAccessTokenInSession(ctx)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.accessToken != "" && a.accessTokenExpiration.After(time.Now()) {
		return a.accessToken, nil
	}
	token, expiration, err := loadToken(a.dbPath+accessTokenDb, a.Profile)
	if err != nil || time.Since(expiration) > -a.ExpirationBuffer {
		// Get a new access token
		return a.fetchAccessToken(ctx)
	}
	a.accessToken = token
	a.accessTokenExpiration = expiration
	return token, nil
}

func (a *Authenticator) getAccessTokenInSession(ctx context.Context) (string, error) {
	path := "/run/velda/.token"
	token, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", NotLoginnedError
	}
	if err != nil {
		return "", err
	}
	return string(token), nil
}

func (a *Authenticator) fetchAccessToken(ctx context.Context) (string, error) {
	refreshToken, err := a.getRefreshTokenLocked(ctx)
	if err != nil {
		return "", err
	}
	resp, err := a.authClient.IssueToken(ctx, &proto.IssueTokenRequest{
		RefreshToken: refreshToken,
	})
	if err != nil {
		return "", err
	}
	token := resp.AccessToken
	exp := resp.AccessTokenExpiresAt.AsTime()
	err = saveToken(a.dbPath+accessTokenDb, token, a.Profile, exp)
	if err != nil {
		return "", err
	}
	a.accessToken = token
	a.accessTokenExpiration = exp
	return resp.AccessToken, nil
}

func saveToken(dbPath string, token string, profile string, expiration time.Time) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS tokens(profile TEXT PRIMARY KEY, token TEXT, expiration TIMESTAMP NOT NULL)")
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO tokens(token, profile, expiration) VALUES($1, $2, $3) ON CONFLICT(profile) DO UPDATE SET token = $1, expiration = $3", token, profile, expiration)
	return err
}

func loadToken(dbPath string, profile string) (string, time.Time, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", time.Time{}, err
	}
	defer db.Close()
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS tokens(profile TEXT PRIMARY KEY, token TEXT, expiration TIMESTAMP NOT NULL)")
	if err != nil {
		return "", time.Time{}, err
	}
	var token string
	var expiration time.Time
	err = db.QueryRow("SELECT token, expiration FROM tokens WHERE profile = $1", profile).Scan(&token, &expiration)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiration, nil
}

func removeToken(dbPath string, profile string) error {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec("DELETE FROM tokens WHERE profile = $1", profile)
	if err != nil {
		// Ignore "no such table" error
		if err.Error() == "no such table: tokens" {
			return nil
		}
		return err
	}
	return nil
}
