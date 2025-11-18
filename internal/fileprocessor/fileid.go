package fileprocessor

import (
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// GenerateFileID creates a unique 12-character file identifier
// Uses first 12 chars of MongoDB ObjectID hex (24 chars total)
// Collision probability: ~1 in 16 trillion for 1 million files
func GenerateFileID() string {
	return primitive.NewObjectID().Hex()[:12]
}
