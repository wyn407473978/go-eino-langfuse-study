package req

type ModelChatReq struct {
	Message   string `json:"message" binding:"required"`
	UserID    string `json:"userId" binding:"required"`
	SessionID string `json:"sessionId" binding:"required"`
}

type ModelChatResp struct {
	Answer string `json:"answer"`
}
