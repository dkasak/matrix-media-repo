package custom

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/turt2live/matrix-media-repo/api"
	"github.com/turt2live/matrix-media-repo/common"
	"github.com/turt2live/matrix-media-repo/controllers/maintenance_controller"
	"github.com/turt2live/matrix-media-repo/matrix"
	"github.com/turt2live/matrix-media-repo/storage"
	"github.com/turt2live/matrix-media-repo/types"
	"github.com/turt2live/matrix-media-repo/util"
)

type MediaPurgedResponse struct {
	NumRemoved int `json:"total_removed"`
}

func PurgeRemoteMedia(r *http.Request, log *logrus.Entry, user api.UserInfo) interface{} {
	beforeTsStr := r.URL.Query().Get("before_ts")
	if beforeTsStr == "" {
		return api.BadRequest("Missing before_ts argument")
	}
	beforeTs, err := strconv.ParseInt(beforeTsStr, 10, 64)
	if err != nil {
		return api.BadRequest("Error parsing before_ts: " + err.Error())
	}

	log = log.WithFields(logrus.Fields{
		"beforeTs": beforeTs,
	})

	// We don't bother clearing the cache because it's still probably useful there
	removed, err := maintenance_controller.PurgeRemoteMediaBefore(beforeTs, r.Context(), log)
	if err != nil {
		log.Error("Error purging remote media: " + err.Error())
		return api.InternalServerError("Error purging remote media")
	}

	return &api.DoNotCacheResponse{Payload: &MediaPurgedResponse{NumRemoved: removed}}
}

func PurgeIndividualRecord(r *http.Request, log *logrus.Entry, user api.UserInfo) interface{} {
	isGlobalAdmin, isLocalAdmin := getPurgeRequestInfo(r, log, user)
	localServerName := r.Host

	params := mux.Vars(r)

	server := params["server"]
	mediaId := params["mediaId"]

	log = log.WithFields(logrus.Fields{
		"server":  server,
		"mediaId": mediaId,
	})

	// If the user is NOT a global admin, ensure they are speaking to the right server
	if !isGlobalAdmin {
		if server != localServerName {
			return api.AuthFailed()
		}
		// If the user is NOT a local admin, ensure they uploaded the content in the first place
		if !isLocalAdmin {
			db := storage.GetDatabase().GetMediaStore(r.Context(), log)
			m, err := db.Get(server, mediaId)
			if err == sql.ErrNoRows {
				return api.NotFoundError()
			}
			if err != nil {
				log.Error("Error checking ownership of media: " + err.Error())
				return api.InternalServerError("error checking media ownership")
			}
			if m.UserId != user.UserId {
				return api.AuthFailed()
			}
		}
	}

	err := maintenance_controller.PurgeMedia(server, mediaId, r.Context(), log)
	if err == sql.ErrNoRows || err == common.ErrMediaNotFound {
		return api.NotFoundError()
	}
	if err != nil {
		log.Error("Error purging media: " + err.Error())
		return api.InternalServerError("error purging media")
	}

	return &api.DoNotCacheResponse{Payload: map[string]interface{}{"purged": true}}
}

func PurgeQuarantined(r *http.Request, log *logrus.Entry, user api.UserInfo) interface{} {
	isGlobalAdmin, isLocalAdmin := getPurgeRequestInfo(r, log, user)
	localServerName := r.Host

	var affected []*types.Media
	var err error

	if isGlobalAdmin {
		affected, err = maintenance_controller.PurgeQuarantined(r.Context(), log)
	} else if isLocalAdmin {
		affected, err = maintenance_controller.PurgeQuarantinedFor(localServerName, r.Context(), log)
	} else {
		return api.AuthFailed()
	}

	if err != nil {
		log.Error("Error purging media: " + err.Error())
		return api.InternalServerError("error purging media")
	}

	mxcs := make([]string, 0)
	for _, a := range affected {
		mxcs = append(mxcs, a.MxcUri())
	}

	return &api.DoNotCacheResponse{Payload: map[string]interface{}{"purged": true, "affected": mxcs}}
}

func PurgeOldMedia(r *http.Request, log *logrus.Entry, user api.UserInfo) interface{} {
	var err error
	beforeTs := util.NowMillis()
	beforeTsStr := r.URL.Query().Get("before_ts")
	if beforeTsStr != "" {
		beforeTs, err = strconv.ParseInt(beforeTsStr, 10, 64)
		if err != nil {
			return api.BadRequest("Error parsing before_ts: " + err.Error())
		}
	}

	includeLocal := false
	includeLocalStr := r.URL.Query().Get("include_local")
	if includeLocalStr != "" {
		includeLocal, err = strconv.ParseBool(includeLocalStr)
		if err != nil {
			return api.BadRequest("Error parsing include_local: " + err.Error())
		}
	}

	log = log.WithFields(logrus.Fields{
		"before_ts": beforeTs,
		"include_local": includeLocal,
	})

	affected, err := maintenance_controller.PurgeOldMedia(beforeTs, includeLocal, r.Context(), log)

	if err != nil {
		log.Error("Error purging media: " + err.Error())
		return api.InternalServerError("error purging media")
	}

	mxcs := make([]string, 0)
	for _, a := range affected {
		mxcs = append(mxcs, a.MxcUri())
	}

	return &api.DoNotCacheResponse{Payload: map[string]interface{}{"purged": true, "affected": mxcs}}
}

func PurgeUserMedia(r *http.Request, log *logrus.Entry, user api.UserInfo) interface{} {
	isGlobalAdmin, isLocalAdmin := getPurgeRequestInfo(r, log, user)
	if !isGlobalAdmin && !isLocalAdmin {
		return api.AuthFailed()
	}

	var err error
	beforeTs := util.NowMillis()
	beforeTsStr := r.URL.Query().Get("before_ts")
	if beforeTsStr != "" {
		beforeTs, err = strconv.ParseInt(beforeTsStr, 10, 64)
		if err != nil {
			return api.BadRequest("Error parsing before_ts: " + err.Error())
		}
	}

	params := mux.Vars(r)

	userId := params["userId"]

	log = log.WithFields(logrus.Fields{
		"userId":   userId,
		"beforeTs": beforeTs,
	})

	_, userDomain, err := util.SplitUserId(userId)
	if err != nil {
		log.Error("Error parsing user ID (" + userId + "): " + err.Error())
		return api.InternalServerError("error parsing user ID")
	}

	if !isGlobalAdmin && userDomain != r.Host {
		return api.AuthFailed()
	}

	affected, err := maintenance_controller.PurgeUserMedia(userId, beforeTs, r.Context(), log)

	if err != nil {
		log.Error("Error purging media: " + err.Error())
		return api.InternalServerError("error purging media")
	}

	mxcs := make([]string, 0)
	for _, a := range affected {
		mxcs = append(mxcs, a.MxcUri())
	}

	return &api.DoNotCacheResponse{Payload: map[string]interface{}{"purged": true, "affected": mxcs}}
}

func PurgeRoomMedia(r *http.Request, log *logrus.Entry, user api.UserInfo) interface{} {
	isGlobalAdmin, isLocalAdmin := getPurgeRequestInfo(r, log, user)
	if !isGlobalAdmin && !isLocalAdmin {
		return api.AuthFailed()
	}

	var err error
	beforeTs := util.NowMillis()
	beforeTsStr := r.URL.Query().Get("before_ts")
	if beforeTsStr != "" {
		beforeTs, err = strconv.ParseInt(beforeTsStr, 10, 64)
		if err != nil {
			return api.BadRequest("Error parsing before_ts: " + err.Error())
		}
	}

	params := mux.Vars(r)

	roomId := params["roomId"]

	log = log.WithFields(logrus.Fields{
		"roomId":   roomId,
		"beforeTs": beforeTs,
	})

	allMedia, err := matrix.ListMedia(r.Context(), r.Host, user.AccessToken, roomId, r.RemoteAddr)
	if err != nil {
		log.Error("Error while listing media in the room: " + err.Error())
		return api.InternalServerError("error retrieving media in room")
	}

	mxcs := make([]string, 0)
	if !isGlobalAdmin {
		for _, mxc := range allMedia.LocalMxcs {
			domain, _, err := util.SplitMxc(mxc)
			if err != nil {
				continue
			}
			if domain != r.Host {
				continue
			}
			mxcs = append(mxcs, mxc)
		}

		for _, mxc := range allMedia.RemoteMxcs {
			domain, _, err := util.SplitMxc(mxc)
			if err != nil {
				continue
			}
			if domain != r.Host {
				continue
			}
			mxcs = append(mxcs, mxc)
		}
	} else {
		for _, mxc := range allMedia.LocalMxcs {
			mxcs = append(mxcs, mxc)
		}
		for _, mxc := range allMedia.RemoteMxcs {
			mxcs = append(mxcs, mxc)
		}
	}

	affected, err := maintenance_controller.PurgeRoomMedia(mxcs, beforeTs, r.Context(), log)

	if err != nil {
		log.Error("Error purging media: " + err.Error())
		return api.InternalServerError("error purging media")
	}

	mxcs = make([]string, 0)
	for _, a := range affected {
		mxcs = append(mxcs, a.MxcUri())
	}

	return &api.DoNotCacheResponse{Payload: map[string]interface{}{"purged": true, "affected": mxcs}}
}

func getPurgeRequestInfo(r *http.Request, log *logrus.Entry, user api.UserInfo) (bool, bool) {
	isGlobalAdmin := util.IsGlobalAdmin(user.UserId) || user.IsShared
	isLocalAdmin, err := matrix.IsUserAdmin(r.Context(), r.Host, user.AccessToken, r.RemoteAddr)
	if err != nil {
		log.Error("Error verifying local admin: " + err.Error())
		return isGlobalAdmin, false
	}

	return isGlobalAdmin, isLocalAdmin
}