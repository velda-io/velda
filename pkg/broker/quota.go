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
package broker

import (
	"context"
	"time"
)

// QuotaGrant represents an allocated quota for a session.
type QuotaGrant struct {
	// New granted total time.
	NextCheck    time.Duration
	TotalGranted time.Duration
	// Pool this grant is for
	Pool string
	// GrantID is a unique identifier for this grant
	GrantID string
	// BalanceDeducted is the amount deducted from quota.
	BalanceDeducted int64
}

// AlwaysAllowQuotaChecker is the OSS dummy implementation that always permits actions.
type AlwaysAllowQuotaChecker struct{}

func (a *AlwaysAllowQuotaChecker) GrantQuota(ctx context.Context, pool string, previousGrant *QuotaGrant, totalConsumedTime time.Duration) (*QuotaGrant, error) {
	return &QuotaGrant{
		NextCheck:       1 * time.Hour,
		TotalGranted:    1 * time.Hour,
		Pool:            pool,
		BalanceDeducted: 0,
		GrantID:         "oss-unlimited",
	}, nil
}

func (a *AlwaysAllowQuotaChecker) ReturnQuota(ctx context.Context, grant *QuotaGrant, actualUsageDuration time.Duration) error {
	return nil
}
