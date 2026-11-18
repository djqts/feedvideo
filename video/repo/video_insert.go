package repo

import (
	"context"
	"feedvideo.local/video/model"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Preserve duplicate-key replay, but require the actual acknowledged n=1
// response before authorizing new-video initialization.
func (r *VideoRepoMongo) insertNewVideo(ctx context.Context, video *model.Video) error {
	if r == nil || r.mdb == nil || r.mdb.Col() == nil {
		return ErrPublishUnavailable
	}
	db := r.mdb.Col().Database()
	command := bson.D{{Key: "insert", Value: r.mdb.Col().Name()}, {Key: "documents", Value: bson.A{video}}, {Key: "ordered", Value: true}}
	if wc := db.WriteConcern(); wc != nil {
		if !wc.Acknowledged() {
			return ErrPublishUnavailable
		}
		command = append(command, bson.E{Key: "writeConcern", Value: wc})
	}
	raw, err := db.RunCommand(ctx, command).Raw()
	if err != nil {
		return err
	}
	if _, err := raw.LookupErr("writeConcernError"); err == nil {
		return ErrPublishUnavailable
	}
	if value, err := raw.LookupErr("writeErrors"); err == nil {
		array, ok := value.ArrayOK()
		if !ok {
			return ErrPublishUnavailable
		}
		values, err := array.Values()
		if err != nil {
			return ErrPublishUnavailable
		}
		if len(values) > 0 {
			var writes mongo.WriteErrors
			for _, value := range values {
				doc, ok := value.DocumentOK()
				if !ok {
					return ErrPublishUnavailable
				}
				var write mongo.WriteError
				if err := bson.Unmarshal(doc, &write); err != nil || write.Code <= 0 {
					return ErrPublishUnavailable
				}
				writes = append(writes, write)
			}
			return mongo.WriteException{WriteErrors: writes}
		}
	}
	if n, ok := raw.Lookup("n").Int32OK(); ok {
		if n != 1 {
			return ErrPublishUnavailable
		}
	} else if n, ok := raw.Lookup("n").Int64OK(); !ok || n != 1 {
		return ErrPublishUnavailable
	}
	return nil
}
