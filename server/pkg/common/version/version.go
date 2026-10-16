// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package version

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"time"
)

// Get returns the overall codebase version. It's for detecting
// what code a binary was built from.
func Get() Info {
	// These variables typically come from -ldflags settings and in
	// their absence fallback to the settings in ./base.go
	return Info{
		Major:        gitMajor,
		Minor:        gitMinor,
		GitVersion:   gitVersion,
		GitTreeState: gitTreeState,
		GitCommit:    gitCommit,
		BuildDate:    buildDate,
		GoVersion:    runtime.Version(),
		Compiler:     runtime.Compiler,
		Platform:     fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}

// GetClientVersion returns the git version of the OpenIM client repository.
func GetClientVersion() (*OpenIMClientVersion, error) {
	clientVersion, err := getClientVersion()
	if err != nil {
		return nil, err
	}
	return &OpenIMClientVersion{
		ClientVersion: clientVersion,
	}, nil
}

func getClientVersion() (string, error) {
	return fetchClientVersion(&http.Client{Timeout: 5 * time.Second}, "https://api.github.com/repos/OpenIMSDK/openim-sdk-core/commits/HEAD")
}

// Reading the commit does not require cloning or writing a shared /tmp checkout.
// This also removes the abandoned Git/SSH stack from the server dependency graph.
func fetchClientVersion(client *http.Client, endpoint string) (string, error) {
	resp, err := client.Get(endpoint)
	if err != nil {
		return "", fmt.Errorf("request SDK version: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("SDK version HTTP status %d", resp.StatusCode)
	}
	var result struct {
		SHA string `json:"sha"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&result); err != nil {
		return "", fmt.Errorf("parse SDK version: %w", err)
	}
	if decoded, err := hex.DecodeString(result.SHA); err != nil || len(decoded) != 20 {
		return "", fmt.Errorf("invalid SDK commit hash")
	}
	return result.SHA, nil
}

// GetSingleVersion returns single version of sealer.
func GetSingleVersion() string {
	return gitVersion
}
