package model

type ExternalOutbound struct {
	ID         int    `json:"id" gorm:"primaryKey;autoIncrement"`
	Tag        string `json:"tag" gorm:"uniqueIndex;not null"`
	Remark     string `json:"remark"`
	Protocol   string `json:"protocol"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"-"`
	ServerName string `json:"serverName"`
	Transport  string `json:"transport"`
	Enable     bool   `json:"enable"`
}
