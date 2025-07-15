package main

import (
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"

	"github.com/bootdotdev/learn-file-storage-s3-golang-starter/internal/auth"
	"github.com/google/uuid"
)

func (cfg *apiConfig) handlerUploadThumbnail(w http.ResponseWriter, r *http.Request) {
	videoIDString := r.PathValue("videoID")
	videoID, err := uuid.Parse(videoIDString)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid ID", err)
		return
	}

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't find JWT", err)
		return
	}

	userID, err := auth.ValidateJWT(token, cfg.jwtSecret)
	if err != nil {
		respondWithError(w, http.StatusUnauthorized, "Couldn't validate JWT", err)
		return
	}


	fmt.Println("uploading thumbnail for video", videoID, "by user", userID)

	// TODO: implement the upload here

	const maxMemory = 10 << 20 // 10 MB

	err = r.ParseMultipartForm(maxMemory)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't parse request", err)
		return
	}

	mpfile, header, err := r.FormFile("thumbnail")
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Couldn't get thumbnail from request", err)
		return
	}
	contentType := header.Header.Get("Content-Type")

	video, err := cfg.db.GetVideo(videoID)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Can't find video info", err)
		return
	}

	if video.UserID != userID {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "can't get media type", err)
		return
	}

	if mediaType != "image/jpeg" && mediaType != "image/png" {
		respondWithError(w, http.StatusBadRequest, "invalid file", nil)
		return
	}

	exts, err := mime.ExtensionsByType(contentType) 
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Can't get ext", err)
		return
	}

	ext := ""
	if exts != nil {
		ext = exts[0]
	} else {
		log.Println("Unknown extension!")
	}
	filename := fmt.Sprintf("%v.%v", videoID.String(), ext)
	path := filepath.Join(cfg.assetsRoot, filename)

	file, err := os.Create(path)
	defer file.Close()

	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "can't create file", err)
		return
	}
	if _, err := io.Copy(file, mpfile); err != nil {
		log.Println("file write err")
		respondWithError(w, http.StatusInternalServerError, "can't copy file", err)
		return
	}

	thumbnailUrl := fmt.Sprintf("http://localhost:8091/assets/%v", filename)
	video.ThumbnailURL = &thumbnailUrl

	if err := cfg.db.UpdateVideo(video); err != nil {
		respondWithError(w, http.StatusInternalServerError, "can't save video metadata", err)
		return
	}

	respondWithJSON(w, http.StatusOK, video)
}
