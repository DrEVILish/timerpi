// Package deps pins the dependency set for TimerPi. Do NOT delete this file:
// it keeps go.mod requirements alive between tidy passes.
package deps

import (
	_ "github.com/gin-gonic/gin"
	_ "github.com/gorilla/websocket"
	_ "github.com/grandcat/zeroconf"
	_ "github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	_ "github.com/skip2/go-qrcode"
	_ "github.com/xuri/excelize/v2"
	_ "golang.org/x/sys/unix"
)
