package models

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// Add FileID to existing models

// Updated DriveAccount - add DriveID field
// Updated UploadSession - add FileID field
// Updated KeyFile - add FileID field

// DriveManifest is stored as 2xpfm.manifest on each Google Drive
type DriveManifest struct {
	DriveID   string         `json:"drive_id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Files     []ManifestFile `json:"files"`
}

// ManifestFile represents a file stored on the drive
type ManifestFile struct {
	FileID           string          `json:"file_id"`
	OriginalFilename string          `json:"original_filename"`
	UploadedAt       time.Time       `json:"uploaded_at"`
	Chunks           []ManifestChunk `json:"chunks"`
}

// ManifestChunk represents a chunk stored on the drive
type ManifestChunk struct {
	ChunkID     int    `json:"chunk_id"`
	Filename    string `json:"filename"`
	DriveFileID string `json:"drive_file_id"`
	Size        int64  `json:"size"`
	Checksum    string `json:"checksum"`
}

// StoredFile tracks files stored across multiple drives in MongoDB
type StoredFile struct {
	ID               primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	FileID           string             `bson:"file_id" json:"file_id"`
	UserID           primitive.ObjectID `bson:"user_id" json:"user_id"`
	OriginalFilename string             `bson:"original_filename" json:"original_filename"`
	OriginalSize     int64              `bson:"original_size" json:"original_size"`
	ProcessedSize    int64              `bson:"processed_size" json:"processed_size"`
	Chunks           []StoredChunk      `bson:"chunks" json:"chunks"`
	ObfuscationSeed  string             `bson:"obfuscation_seed" json:"-"` // Not exposed in JSON
	CreatedAt        time.Time          `bson:"created_at" json:"created_at"`
	Status           string             `bson:"status" json:"status"` // "active" | "incomplete" | "deleted"
}

// StoredChunk represents a chunk location in the database
type StoredChunk struct {
	ChunkID        int                `bson:"chunk_id" json:"chunk_id"`
	DriveAccountID primitive.ObjectID `bson:"drive_account_id" json:"drive_account_id"`
	DriveID        string             `bson:"drive_id" json:"drive_id"`
	DriveFileID    string             `bson:"drive_file_id" json:"drive_file_id"`
	Filename       string             `bson:"filename" json:"filename"`
	Size           int64              `bson:"size" json:"size"`
	Checksum       string             `bson:"checksum" json:"checksum"`
	StartOffset    int64              `bson:"start_offset" json:"start_offset"`
	EndOffset      int64              `bson:"end_offset" json:"end_offset"`
}

// DownloadSession tracks file download/decryption progress
type DownloadSession struct {
	ID                primitive.ObjectID `bson:"_id,omitempty" json:"id"`
	UserID            primitive.ObjectID `bson:"user_id" json:"user_id"`
	FileID            string             `bson:"file_id" json:"file_id"`
	OriginalFilename  string             `bson:"original_filename" json:"original_filename"`
	Status            string             `bson:"status" json:"status"` // "downloading"|"decrypting"|"complete"|"failed"
	Progress          float64            `bson:"progress" json:"progress"`
	ErrorMessage      string             `bson:"error_message,omitempty" json:"error_message,omitempty"`
	TempFilePath      string             `bson:"temp_file_path" json:"-"`
	ReconstructedPath string             `bson:"reconstructed_path" json:"-"` // Path to final decrypted file
	CreatedAt         time.Time          `bson:"created_at" json:"created_at"`
	CompletedAt       *time.Time         `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	ExpiresAt         time.Time          `bson:"expires_at" json:"expires_at"`
}
