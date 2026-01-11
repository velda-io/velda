// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sandboxfs

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// xattrCacheName is the extended attribute name used to store SHA256 hash and mtime
	xattrCacheName = "user.veldafs.cache"
)

// encodeCacheXattr encodes SHA256 hash and mtime into xattr format: "version:sha256:mtime_sec.mtime_nsec"
func encodeCacheXattr(sha256sum string, mtime time.Time, size int64) string {
	return fmt.Sprintf("1:%s:%d.%d:%d", sha256sum, mtime.Unix(), mtime.Nanosecond(), size)
}

// decodeCacheXattr decodes xattr value into SHA256 hash and mtime
func decodeCacheXattr(xattrValue string) (sha256sum string, mtime time.Time, size int64, ok bool) {
	parts := strings.Split(xattrValue, ":")
	if len(parts) != 4 || len(parts[1]) != 64 || parts[0] != "1" {
		return "", time.Time{}, 0, false
	}

	sha256sum = parts[1]
	timeParts := strings.Split(parts[2], ".")
	if len(timeParts) != 2 {
		return "", time.Time{}, 0, false
	}

	sec, err := strconv.ParseInt(timeParts[0], 10, 64)
	if err != nil {
		return "", time.Time{}, 0, false
	}

	nsec, err := strconv.ParseInt(timeParts[1], 10, 64)
	if err != nil {
		return "", time.Time{}, 0, false
	}

	mtime = time.Unix(sec, nsec)

	size, err = strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return "", time.Time{}, 0, false
	}

	return sha256sum, mtime, size, true
}
