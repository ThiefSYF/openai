package handler

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"openai/internal/config"
	"openai/internal/service/fiter"
	"openai/internal/service/openai"
	"openai/internal/service/wechat"
	"sync"
	"time"
)

var (
	success  = []byte("success")
	warn     = "警告，检测到敏感词"
	requests sync.Map // K - 消息ID ， V - chan string
	// users    sync.Map
)

func WechatCheck(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	signature := query.Get("signature")
	timestamp := query.Get("timestamp")
	nonce := query.Get("nonce")
	echostr := query.Get("echostr")

	// 校验
	if wechat.CheckSignature(signature, timestamp, nonce, config.WxToken) {
		w.Write([]byte(echostr))
		return
	}

	log.Println("此接口为公众号验证，不应该被手动调用，公众号接入校验失败")
}

// https://developers.weixin.qq.com/doc/offiaccount/Message_Management/Passive_user_reply_message.html
// 微信服务器在五秒内收不到响应会断掉连接，并且重新发起请求，总共重试三次
func ReceiveMsg(w http.ResponseWriter, r *http.Request) {
	bs, _ := io.ReadAll(r.Body)
	msg := wechat.NewMsg(bs)

	if msg == nil {
		echo(w, []byte("xml格式公众号消息接口，请勿手动调用"))
		return
	}

	// 非文本不回复(返回success表示不回复)
	if !msg.IsText() {
		log.Println("非文本不回复")
		echo(w, success)
		return
	}

	// 敏感词检测
	if !fiter.Check(msg.Content) {
		warnWx := msg.GenerateEchoData(warn)
		echo(w, warnWx)
		return
	}

	var ch chan string
	v, ok := requests.Load(msg.MsgId)
	if !ok {
		log.Println("Q:", msg.Content)
		ch = make(chan string)
		requests.Store(msg.MsgId, ch)
		go func(id int64, user_name string, msg string) {
			// 15s不回复微信，则失效
			result := openai.Query(msg, user_name, time.Second*14)
			ch <- result
		}(msg.MsgId, msg.FromUserName, msg.Content)
	} else {
		ch = v.(chan string)
	}

	select {
	case result := <-ch:
		if !fiter.Check(result) {
			result = warn
		}
		bs := msg.GenerateEchoData(result)
		echo(w, bs)
		close(ch)
		requests.Delete(msg.MsgId)
	// 超时不要回答，会重试的
	case <-time.After(time.Second * 5):
	}
}

func Test(w http.ResponseWriter, r *http.Request) {
	msg := r.URL.Query().Get("msg")
	if !fiter.Check(msg) {
		echoJson(w, "", warn)
		return
	}
	s := openai.Query(msg, "test", time.Second*180)
	echoJson(w, s, "")
}

func echoJson(w http.ResponseWriter, replyMsg string, errMsg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	var code int
	var message = replyMsg
	if errMsg != "" {
		code = -1
		message = errMsg
	}
	data, _ := json.Marshal(map[string]interface{}{
		"code":    code,
		"message": message,
	})
	w.Write(data)
}

func echo(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}
