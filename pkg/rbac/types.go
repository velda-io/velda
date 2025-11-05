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
package rbac

import "context"

type User interface {
	isUser()
}

type EmptyUser struct{}

func (EmptyUser) isUser() {}

type Action string

type Permissions interface {
	// Check verifies if the user has permission to perform the action on the resource.
	Check(ctx context.Context, action Action, resource string) error

	// Return a list of search keys that can be used for labeling & filtering.
	// The keys should be in the format "key=value".
	SearchKeys(ctx context.Context) []string
}

type AlwaysAllowPermissions struct{}

func (a *AlwaysAllowPermissions) Check(ctx context.Context, action Action, resource string) error {
	return nil
}

func (a *AlwaysAllowPermissions) SearchKeys(ctx context.Context) []string {
	return []string{}
}

type SessionUser interface {
	User
	TaskId() string
}

var userKey = &struct{}{}

func UserFromContext(ctx context.Context) User {
	user, ok := ctx.Value(userKey).(User)
	if !ok {
		panic("Accessing user without auth interceptor")
	}
	return user
}

func ContextWithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, userKey, user)
}
