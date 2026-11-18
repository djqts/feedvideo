package main

import (
	"context"

	creatorservice "feedvideo.local/creator-center/service"
	usermodel "feedvideo.local/user/model"
)

type verificationAccountReader interface {
	GetUserByID(context.Context, int64) (*usermodel.User, error)
}

func configureCreatorVerificationTarget(s *creatorservice.CreatorService, reader verificationAccountReader) {
	s.SetVerificationTargetReader(reader.GetUserByID)
}
