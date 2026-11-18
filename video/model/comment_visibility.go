package model

// Reported comments remain visible pending review; missing legacy status is
// retained. Hidden and unfamiliar states never grant comment access.
func IsVisibleComment(comment *Comment) bool {
	return comment != nil && (comment.Status == "" || comment.Status == "active" || comment.Status == "reported")
}
