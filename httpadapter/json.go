package httpadapter

import (
	"encoding/json"
	"net/http"

	"google.golang.org/grpc/codes"

	"github.com/Linuxea/protoc-gen-gin/ginruntime"
)

func writeError(w http.ResponseWriter, httpStatus int, code codes.Code, message string) {
	writeJSON(w, httpStatus, ginruntime.Error{Code: int32(code), Message: message})
}

func writeJSON(w http.ResponseWriter, httpStatus int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(v)
}
