package thumbnail_service

import (
	"bytes"
	"context"
	"errors"

	"github.com/disintegration/imaging"
	"github.com/sirupsen/logrus"
	"github.com/turt2live/matrix-media-repo/storage"
	"github.com/turt2live/matrix-media-repo/types"
	"github.com/turt2live/matrix-media-repo/util"
)

type generatedThumbnail struct {
	ContentType  string
	DiskLocation string
	SizeBytes    int64
	Animated     bool
}

type thumbnailer struct {
	ctx context.Context
	log *logrus.Entry
}

func NewThumbnailer(ctx context.Context, log *logrus.Entry) *thumbnailer {
	return &thumbnailer{ctx, log}
}

func (t *thumbnailer) GenerateThumbnail(media *types.Media, width int, height int, method string, animated bool, forceGeneration bool) (*generatedThumbnail, error) {
	if animated && !util.ArrayContains(animatedTypes, media.ContentType) {
		t.log.Warn("Attempted to animate a media record that isn't an animated type. Assuming animated=false")
		animated = false
	}

	src, err := imaging.Open(media.Location)
	if err != nil {
		return nil, err
	}

	srcWidth := src.Bounds().Max.X
	srcHeight := src.Bounds().Max.Y

	aspectRatio := float32(srcHeight) / float32(srcWidth)
	targetAspectRatio := float32(width) / float32(height)
	if aspectRatio == targetAspectRatio {
		// Highly unlikely, but if the aspect ratios match then just resize
		method = "scale"
		t.log.Info("Aspect ratio is the same, converting method to 'scale'")
	}

	thumb := &generatedThumbnail{
		Animated: animated,
	}

	if srcWidth <= width && srcHeight <= height {
		if forceGeneration {
			t.log.Warn("Image is too small but the force flag is set. Adjusting dimensions to fit image exactly.")
			width = srcWidth
			height = srcHeight
		} else {
			// Image is too small - don't upscale
			thumb.ContentType = media.ContentType
			thumb.DiskLocation = media.Location
			thumb.SizeBytes = media.SizeBytes
			t.log.Warn("Image too small, returning raw image")
			return thumb, nil
		}
	}

	if method == "scale" {
		src = imaging.Fit(src, width, height, imaging.Lanczos)
	} else if method == "crop" {
		src = imaging.Fill(src, width, height, imaging.Center, imaging.Lanczos)
	} else {
		t.log.Error("Unrecognized thumbnail method: " + method)
		return nil, errors.New("unrecognized method: " + method)
	}

	// Put the image bytes into a memory buffer
	imgData := &bytes.Buffer{}
	err = imaging.Encode(imgData, src, imaging.PNG)
	if err != nil {
		t.log.Error("Unexpected error encoding thumbnail: " + err.Error())
		return nil, err
	}

	// Reset the buffer pointer and store the file
	location, err := storage.PersistFile(imgData, t.ctx)
	if err != nil {
		t.log.Error("Unexpected error saving thumbnail: " + err.Error())
		return nil, err
	}

	fileSize, err := util.FileSize(location)
	if err != nil {
		t.log.Error("Unexpected error getting the size of the thumbnail: " + err.Error())
		return nil, err
	}

	thumb.DiskLocation = location
	thumb.ContentType = "image/png"
	thumb.SizeBytes = fileSize

	return thumb, nil
}
