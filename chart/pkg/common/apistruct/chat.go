// Copyright © 2023 OpenIM open source community. All rights reserved.
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

package apistruct

type UserRegisterResp struct {
	AdminRequestID        string `json:"adminRequestID,omitempty"`
	ProvisioningReceiptID string `json:"provisioningReceiptID,omitempty"`
	ProvisioningState     string `json:"provisioningState,omitempty"`
	ProvisioningPending   bool   `json:"provisioningPending,omitempty"`
	Recovered             bool   `json:"recovered,omitempty"`
	ImToken               string `json:"imToken"`
	ChatToken             string `json:"chatToken"`
	UserID                string `json:"userID"`
}

type LoginResp struct {
	ImToken   string `json:"imToken"`
	ChatToken string `json:"chatToken"`
	UserID    string `json:"userID"`
}

type UpdateUserInfoResp struct {
	IMSyncPending bool `json:"imSyncPending"`
}
