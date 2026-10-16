package apicall

// OpenIM presence wrappers use primitive JSON values. Pointers preserve an
// explicit empty avatar while chart keeps its existing protocol module version.
type profileInfo struct {
	UserID   string  `json:"userID"`
	Nickname *string `json:"nickname"`
	FaceURL  *string `json:"faceURL"`
}
type profileUpdateReq struct {
	UserInfo *profileInfo `json:"userInfo"`
}
type profileUpdateResp struct{}
