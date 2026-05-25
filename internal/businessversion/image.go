package businessversion

import (
	"strings"

	"github.com/OT-CONTAINER-KIT/redis-operator/internal/envs"
)

func ResolveImage(image, businessVersion string) string {
	if strings.TrimSpace(image) == "" {
		return image
	}
	version := strings.TrimSpace(businessVersion)
	if version == "" {
		return image
	}
	if version == "latest" {
		version = strings.TrimSpace(envs.GetOperatorImageTag())
	}
	if version == "" {
		return image
	}
	return ReplaceImageTag(image, version)
}

func ReplaceImageTag(image, tag string) string {
	imageWithoutDigest := image
	if digestIndex := strings.Index(imageWithoutDigest, "@"); digestIndex >= 0 {
		imageWithoutDigest = imageWithoutDigest[:digestIndex]
	}

	lastSlash := strings.LastIndex(imageWithoutDigest, "/")
	lastColon := strings.LastIndex(imageWithoutDigest, ":")
	if lastColon > lastSlash {
		imageWithoutDigest = imageWithoutDigest[:lastColon]
	}

	return imageWithoutDigest + ":" + tag
}
