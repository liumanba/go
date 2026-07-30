package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// ---------- 消息结构 ----------
type Message struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Time    string `json:"time"`
	Image   string `json:"image,omitempty"`
	Avatar  string `json:"avatar,omitempty"`
}

// ---------- 用户结构 ----------
type User struct {
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
	Avatar       string `json:"avatar,omitempty"`
}

var (
	msgList  []Message
	mu       sync.RWMutex
	dataPath = filepath.Join("./ms", "message.json")

	users       []User
	userMu      sync.RWMutex
	userDataPath = filepath.Join("./ms", "users.json")

	jwtSecret = []byte("your-secret-key-change-me")

	scrollPositions   = make(map[string]int)
	scrollPositionsMu sync.RWMutex
)

// ---------- 初始化 ----------
func initData() {
	_ = os.Mkdir("./ms", 0755)
	_ = os.Mkdir("./uploads", 0755)

	// 加载消息
	f, err := os.Stat(dataPath)
	if os.IsNotExist(err) {
		msgList = make([]Message, 0)
	} else if err != nil {
		log.Printf("读取消息状态失败: %v", err)
	} else if f.Size() > 0 {
		buf, err := os.ReadFile(dataPath)
		if err != nil {
			log.Printf("读取消息文件失败: %v", err)
		} else if err := json.Unmarshal(buf, &msgList); err != nil {
			log.Printf("解析消息 JSON 失败: %v", err)
			msgList = make([]Message, 0)
		}
	} else {
		msgList = make([]Message, 0)
	}

	// 加载用户
	uf, err := os.Stat(userDataPath)
	if os.IsNotExist(err) {
		users = make([]User, 0)
	} else if err != nil {
		log.Printf("读取用户状态失败: %v", err)
	} else if uf.Size() > 0 {
		buf, err := os.ReadFile(userDataPath)
		if err != nil {
			log.Printf("读取用户文件失败: %v", err)
		} else if err := json.Unmarshal(buf, &users); err != nil {
			log.Printf("解析用户 JSON 失败: %v", err)
			users = make([]User, 0)
		}
	} else {
		users = make([]User, 0)
	}
}

// ---------- 持久化 ----------
func saveMsgToFile() {
	mu.RLock()
	data, err := json.MarshalIndent(msgList, "", "  ")
	mu.RUnlock()
	if err != nil {
		log.Printf("序列化消息失败: %v", err)
		return
	}
	if err := os.WriteFile(dataPath, data, 0644); err != nil {
		log.Printf("写入消息文件失败: %v", err)
	}
}

func saveUsersToFile() {
	userMu.RLock()
	data, err := json.MarshalIndent(users, "", "  ")
	userMu.RUnlock()
	if err != nil {
		log.Printf("序列化用户失败: %v", err)
		return
	}
	if err := os.WriteFile(userDataPath, data, 0644); err != nil {
		log.Printf("写入用户文件失败: %v", err)
	}
}

// ---------- 消息 API ----------
func getMessage(c *gin.Context) {
	mu.RLock()
	defer mu.RUnlock()
	c.JSON(http.StatusOK, gin.H{"data": msgList})
}

func sendMessage(c *gin.Context) {
	tokenString := c.GetHeader("Authorization")
	if tokenString == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "请先登录"})
		return
	}
	if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
		tokenString = tokenString[7:]
	} else {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "无效的认证格式"})
		return
	}

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})
	if err != nil || !token.Valid {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "登录已过期，请重新登录"})
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "无效的令牌"})
		return
	}
	username, ok := claims["username"].(string)
	if !ok || username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "无效的用户名"})
		return
	}

	var req struct {
		Content string `json:"content"`
		Image   string `json:"image,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "参数错误"})
		return
	}
	if req.Content == "" && req.Image == "" {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "内容或图片不能为空"})
		return
	}

	// 获取用户头像
	var avatar string
	userMu.RLock()
	for _, u := range users {
		if u.Username == username {
			avatar = u.Avatar
			break
		}
	}
	userMu.RUnlock()

	msg := Message{
		Name:    username,
		Content: req.Content,
		Time:    time.Now().Format("15:04:05"),
		Image:   req.Image,
		Avatar:  avatar,
	}

	mu.Lock()
	msgList = append(msgList, msg)
	mu.Unlock()

	go saveMsgToFile()
	c.JSON(http.StatusOK, gin.H{"msg": "发送成功"})
}

func uploadImage(c *gin.Context) {
	file, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "请选择图片文件"})
		return
	}

	ext := filepath.Ext(file.Filename)
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".gif" && ext != ".webp" {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "仅支持 jpg, jpeg, png, gif, webp 格式"})
		return
	}
	if file.Size > 5<<20 {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "图片大小不能超过 5MB"})
		return
	}

	filename := time.Now().Format("20060102150405") + "_" + filepath.Base(file.Filename)
	savePath := filepath.Join("./uploads", filename)

	if err := c.SaveUploadedFile(file, savePath); err != nil {
		log.Printf("保存图片失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "保存图片失败"})
		return
	}

	url := "/uploads/" + filename
	c.JSON(http.StatusOK, gin.H{"url": url})
}

// ---------- 滚动位置 API ----------
func getScrollPosition(c *gin.Context) {
	username, exists := c.Get("username")
	if !exists || username == "" {
		c.JSON(http.StatusOK, gin.H{"scrollTop": 0})
		return
	}
	scrollPositionsMu.RLock()
	pos := scrollPositions[username.(string)]
	scrollPositionsMu.RUnlock()
	c.JSON(http.StatusOK, gin.H{"scrollTop": pos})
}

func saveScrollPosition(c *gin.Context) {
	username, exists := c.Get("username")
	if !exists || username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "请先登录"})
		return
	}
	var req struct {
		ScrollTop int `json:"scrollTop"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "参数错误"})
		return
	}
	scrollPositionsMu.Lock()
	scrollPositions[username.(string)] = req.ScrollTop
	scrollPositionsMu.Unlock()
	c.JSON(http.StatusOK, gin.H{"msg": "ok"})
}

// ---------- 用户认证 API ----------
func register(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "参数错误"})
		return
	}
	if req.Username == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "用户名和密码不能为空"})
		return
	}
	if len(req.Password) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "密码长度至少6位"})
		return
	}

	userMu.RLock()
	for _, u := range users {
		if u.Username == req.Username {
			userMu.RUnlock()
			c.JSON(http.StatusConflict, gin.H{"msg": "用户名已存在"})
			return
		}
	}
	userMu.RUnlock()

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("密码哈希失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "注册失败"})
		return
	}

	userMu.Lock()
	users = append(users, User{Username: req.Username, PasswordHash: string(hash)})
	userMu.Unlock()

	go saveUsersToFile()
	c.JSON(http.StatusOK, gin.H{"msg": "注册成功"})
}

func login(c *gin.Context) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "参数错误"})
		return
	}
	if req.Username == "" || req.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "用户名和密码不能为空"})
		return
	}

	var foundUser *User
	userMu.RLock()
	for i := range users {
		if users[i].Username == req.Username {
			foundUser = &users[i]
			break
		}
	}
	userMu.RUnlock()

	if foundUser == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "用户名或密码错误"})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(foundUser.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "用户名或密码错误"})
		return
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username": req.Username,
		"exp":      time.Now().Add(24 * time.Hour).Unix(),
	})
	tokenString, err := token.SignedString(jwtSecret)
	if err != nil {
		log.Printf("生成令牌失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "登录失败"})
		return
	}

	// 返回用户头像，方便前端持久化存储
	c.JSON(http.StatusOK, gin.H{
		"msg":      "登录成功",
		"token":    tokenString,
		"username": req.Username,
		"avatar":   foundUser.Avatar,
	})
}

// ---------- 用户资料与头像 ----------
func getUserProfile(c *gin.Context) {
	username, exists := c.Get("username")
	if !exists || username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "请先登录"})
		return
	}
	var avatar string
	userMu.RLock()
	for _, u := range users {
		if u.Username == username {
			avatar = u.Avatar
			break
		}
	}
	userMu.RUnlock()
	c.JSON(http.StatusOK, gin.H{
		"username": username,
		"avatar":   avatar,
	})
}

func updateAvatar(c *gin.Context) {
	usernameVal, exists := c.Get("username")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "请先登录"})
		return
	}
	username, ok := usernameVal.(string)
	if !ok || username == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"msg": "无效的用户名"})
		return
	}

	file, err := c.FormFile("avatar")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "请选择图片文件"})
		return
	}
	ext := filepath.Ext(file.Filename)
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".gif" && ext != ".webp" {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "仅支持 jpg, jpeg, png, gif, webp 格式"})
		return
	}
	if file.Size > 5<<20 {
		c.JSON(http.StatusBadRequest, gin.H{"msg": "图片大小不能超过 5MB"})
		return
	}

	filename := "avatar_" + username + "_" + time.Now().Format("20060102150405") + ext
	savePath := filepath.Join("./uploads", filename)
	if err := c.SaveUploadedFile(file, savePath); err != nil {
		log.Printf("保存头像失败: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"msg": "保存头像失败"})
		return
	}
	url := "/uploads/" + filename

	userMu.Lock()
	for i, u := range users {
		if u.Username == username {
			users[i].Avatar = url
			break
		}
	}
	userMu.Unlock()
	// 同步写入，确保立即持久化
	saveUsersToFile()

	c.JSON(http.StatusOK, gin.H{
		"msg":    "头像更新成功",
		"avatar": url,
	})
}

// ---------- 中间件 ----------
func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := c.GetHeader("Authorization")
		if tokenString == "" {
			c.Next()
			return
		}
		if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
			tokenString = tokenString[7:]
		}
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			return jwtSecret, nil
		})
		if err == nil && token.Valid {
			if claims, ok := token.Claims.(jwt.MapClaims); ok {
				c.Set("username", claims["username"])
			}
		}
		c.Next()
	}
}

// ---------- 主函数 ----------
func main() {
	initData()

	r := gin.Default()
	r.Use(authMiddleware())

	r.Static("/static", "./static")
	r.Static("/uploads", "./uploads")

	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/static/index.html")
	})

	// 消息 API
	r.GET("/message", getMessage)
	r.POST("/send", sendMessage)
	r.POST("/upload", uploadImage)

	// 滚动位置 API
	r.GET("/scroll", getScrollPosition)
	r.POST("/scroll", saveScrollPosition)

	// 用户 API
	r.POST("/register", register)
	r.POST("/login", login)
	r.GET("/profile", getUserProfile)
	r.POST("/avatar", updateAvatar)

	log.Println("服务器启动在 0.0.0.0:8080")
	if err := r.Run("0.0.0.0:8080"); err != nil {
		log.Fatalf("启动失败: %v", err)
	}
}