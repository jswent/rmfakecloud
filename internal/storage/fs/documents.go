package fs

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v4"
	log "github.com/sirupsen/logrus"

	"github.com/ddvk/rmfakecloud/internal/common"
	"github.com/ddvk/rmfakecloud/internal/config"
	"github.com/ddvk/rmfakecloud/internal/storage"
	"github.com/ddvk/rmfakecloud/internal/storage/exporter"
)

// DefaultTrashDir name of the trash dir
const (
	DefaultTrashDir = ".trash"
	CacheDir        = ".cache"
	Archive         = "archive"
	SyncFolder      = "sync"
)

// FileSystemStorage store everything to disk
type FileSystemStorage struct {
	Cfg *config.Config

	exporterOnce sync.Once
	exporter     *exporter.Exporter
}

func sanitizeFileName(fileName string) string {
	return filepath.Clean(filepath.Base(fileName))
}

func (fs *FileSystemStorage) getUserPath(uid string) string {
	return filepath.Join(fs.Cfg.DataDir, filepath.Base(userDir), common.SanitizeUid(uid))
}

// gets the blobstorage path
func (fs *FileSystemStorage) getUserBlobPath(uid string) string {
	return filepath.Join(fs.getUserPath(uid), SyncFolder)
}

func (fs *FileSystemStorage) getPathFromUser(uid, path string) string {
	return filepath.Join(fs.getUserPath(uid), sanitizeFileName(path))
}

// GetDocument Opens a document by id
func (fs *FileSystemStorage) GetDocument(uid, id string) (io.ReadCloser, error) {
	fullPath := fs.getPathFromUser(uid, id+storage.ZipFileExt)
	log.Debugln("Fullpath:", fullPath)
	reader, err := os.Open(fullPath)
	return reader, err
}

// RemoveDocument removes document (moves it to trash)
func (fs *FileSystemStorage) RemoveDocument(uid, id string) error {

	trashDir := fs.getPathFromUser(uid, DefaultTrashDir)
	err := os.MkdirAll(trashDir, 0700)
	if err != nil {
		return err
	}
	//do not delete, move to trash
	log.Info(trashDir)
	meta := filepath.Base(id + storage.MetadataFileExt)
	fullPath := fs.getPathFromUser(uid, meta)
	err = os.Rename(fullPath, path.Join(trashDir, meta))
	if err != nil {
		return err
	}

	zipfile := filepath.Base(id + storage.ZipFileExt)
	fullPath = fs.getPathFromUser(uid, zipfile)
	err = os.Rename(fullPath, path.Join(trashDir, zipfile))
	if err != nil {
		return err
	}
	return nil
}

// StoreDocument stores a document
func (fs *FileSystemStorage) StoreDocument(uid, id string, stream io.ReadCloser) error {
	fullPath := fs.getPathFromUser(uid, id+storage.ZipFileExt)
	file, err := os.Create(fullPath)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(file, stream)
	return err
}

// GetStorageURL the storage url
func (fs *FileSystemStorage) GetStorageURL(uid, id string) (docurl string, expiration time.Time, err error) {
	uploadRL := fs.Cfg.StorageURL
	exp := time.Now().Add(time.Minute * config.ReadStorageExpirationInMinutes)

	log.Debugln("uploadUrl: ", uploadRL)
	claim := &StorageClaim{
		DocumentID: id,
		UserID:     uid,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(exp),
			Audience:  []string{storageUsage},
		},
	}
	signedToken, err := common.SignClaims(claim, fs.Cfg.JWTSecretKey)
	if err != nil {
		return "", exp, err
	}

	return fmt.Sprintf("%s%s/%s", uploadRL, routeStorage, url.QueryEscape(signedToken)), exp, nil
}
