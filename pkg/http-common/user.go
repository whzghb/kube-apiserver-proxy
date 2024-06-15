package http_common

type UserLoginRequest struct {
	Name     string `json:"name" bind:"required"`
	Password string `json:"password" bind:"required"`
}

type UserLoginResponse struct {
	Token string `json:"token"`
}
