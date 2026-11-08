package req

type ModelChatReq struct {
	Message   string `json:"message" binding:"required"`
	UserID    string `json:"user_id" binding:"required"`
	SessionID string `json:"session_id" binding:"required"`
}

type ModelChatResp struct {
	Answer string `json:"answer"`
}
