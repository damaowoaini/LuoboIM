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

package e2e

import (
	"flag"
	"os"
	"testing"

	"github.com/openimsdk/open-im-server/v3/test/e2e/framework/config"
)

// handleFlags sets up all flags and parses the command line.
func handleFlags() {
	config.CopyFlags(config.Flags, flag.CommandLine)
	flag.Parse()
}

func TestMain(m *testing.M) {
	handleFlags()
	os.Exit(m.Run())
}

func TestE2E(t *testing.T) {
	if os.Getenv("LUOBO_RUN_LEGACY_E2E") != "synthetic-fixtures-only" {
		t.Skip("legacy scenario mutates fixed sample users; explicitly configure disposable synthetic services and opt in; it is not a unit test")
	}
	RunE2ETests(t)
}
