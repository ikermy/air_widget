package db

import (
	"air_widget/configs"
	"encoding/json"
	"fmt"
	"log"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

func GetConf() *configs.Conf {
	conf, err := configs.New("D:\\Go\\Marusia\\AiR_Widget\\configs\\cfg.env")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	return conf
}

func TestCheckUserExist(t *testing.T) {
	conf := GetConf()

	userId := uint32(1)
	userSHA := "345be714b0d92aecb0a3c6be556dadd7a27585a14a48d1bf174592a66daba2ec"
	db := New(conf)
	realUserId, err := db.CheckUserExist(userId, userSHA)

	fmt.Println(realUserId)
	if err != nil {
		t.Fatalf("GetOrSetDemoDialog failed: %v", err)
	}
}

func TestGetUserBalance(t *testing.T) {
	db := New(GetConf())

	balance, err := db.GetUserBalance(1)
	if err != nil {
		t.Fatalf("GetUserBalance failed: %v", err)
	}
	if balance >= 0 {
		t.Logf("GetUserBalance success: %v", balance)
	} else {
		t.Fatalf("GetUserBalance failed: %v", balance)
	}
}

func TestReadResponderName(t *testing.T) {
	conf := GetConf()

	respId := uint64(1741366190290252800)
	db := New(conf)
	respData, err := db.ReadResponderName(respId)
	fmt.Println("RESP_DATA", respData)
	if err != nil {
		t.Fatalf("ReadResponderName failed: %v", err)
	}

	var RespData struct {
		Name   string
		RespId uint64
	}

	err = json.Unmarshal(respData, &RespData)
	if err != nil {
		t.Fatalf("failed to unmarshal json: %v", err)
	}

	fmt.Println("RESP_DATA", RespData)
}

func TestGetTread(t *testing.T) {
	conf := GetConf()

	userId := uint32(1)
	responderId := uint64(2)
	db := New(conf)
	tread, err := db.GetOrSetTreadAndResponder(userId, responderId, "TestRESPONDER")
	if err != nil {
		t.Fatalf("GetTread failed: %v", err)
	}
	t.Logf("Tread id: %v", tread)
}

func TestReadDialog(t *testing.T) {
	conf := GetConf()

	dialogId := uint64(29)
	db := New(conf)
	dialog, err := db.ReadDialog(dialogId)
	if err != nil {
		t.Fatalf("ReadDialog failed: %v", err)
	}
	t.Logf("Dialog data: %v", dialog)

}

func TestGetUserGPT(t *testing.T) {
	conf := GetConf()

	userId := uint32(23)
	db := New(conf)
	gpt, err := db.GetUserGPT(userId)
	if err != nil {
		t.Fatalf("GetUserGPT failed: %v", err)
	}
	t.Logf("User GPT: %v", gpt)
}

func TestReadContext(t *testing.T) {
	conf := GetConf()

	dialogId := uint64(21)
	db := New(conf)
	context, err := db.ReadContext(dialogId)
	if err != nil {
		t.Fatalf("ReadContext failed: %v", err)
	}
	t.Logf("User context: %v", context)
}
