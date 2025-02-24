// Copyright 2023 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package sip

import (
	"context"

	"github.com/pion/webrtc/v3"

	"github.com/livekit/protocol/logger"
	lksdk "github.com/livekit/server-sdk-go"

	"github.com/livekit/sip/pkg/config"
	"github.com/livekit/sip/pkg/media"
	"github.com/livekit/sip/pkg/media/opus"
	"github.com/livekit/sip/pkg/media/rtp"
	"github.com/livekit/sip/pkg/mixer"
)

type Room struct {
	room     *lksdk.Room
	mix      *mixer.Mixer
	out      media.SwitchWriter[media.PCM16Sample]
	identity string
}

type lkRoomConfig struct {
	roomName string
	identity string
}

func NewRoom() *Room {
	r := &Room{}
	r.mix = mixer.NewMixer(&r.out, sampleRate)
	return r
}

func (r *Room) Connect(conf *config.Config, roomName, identity, wsUrl, token string) error {
	var (
		err  error
		room *lksdk.Room
	)
	r.identity = identity
	roomCallback := &lksdk.RoomCallback{
		ParticipantCallback: lksdk.ParticipantCallback{
			OnTrackPublished: func(publication *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				if publication.Kind() == lksdk.TrackKindAudio {
					if err := publication.SetSubscribed(true); err != nil {
						logger.Errorw("cannot subscribe to the track", err, "trackID", publication.SID())
					}
				}
			},
			OnTrackSubscribed: func(track *webrtc.TrackRemote, pub *lksdk.RemoteTrackPublication, rp *lksdk.RemoteParticipant) {
				mTrack := r.NewTrack()
				defer mTrack.Close()

				odec, err := opus.Decode(mTrack, sampleRate, channels)
				if err != nil {
					return
				}
				h := rtp.NewMediaStreamIn[opus.Sample](odec)
				_ = rtp.HandleLoop(track, h)
			},
		},
	}

	if wsUrl == "" || token == "" {
		room, err = lksdk.ConnectToRoom(conf.WsUrl,
			lksdk.ConnectInfo{
				APIKey:              conf.ApiKey,
				APISecret:           conf.ApiSecret,
				RoomName:            roomName,
				ParticipantIdentity: identity,
			}, roomCallback, lksdk.WithAutoSubscribe(false))
	} else {
		room, err = lksdk.ConnectToRoomWithToken(wsUrl, token, roomCallback)
	}

	if err != nil {
		return err
	}
	r.room = room
	return nil
}

func ConnectToRoom(conf *config.Config, roomName string, identity string) (*Room, error) {
	r := NewRoom()
	if err := r.Connect(conf, roomName, identity, "", ""); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Room) Output() media.Writer[media.PCM16Sample] {
	return r.out.Get()
}

func (r *Room) SetOutput(out media.Writer[media.PCM16Sample]) {
	if r == nil {
		return
	}
	r.out.Set(out)
}

func (r *Room) Close() error {
	if r.room != nil {
		r.room.Disconnect()
		r.room = nil
	}
	if r.mix != nil {
		r.mix.Stop()
		r.mix = nil
	}
	return nil
}

func (r *Room) NewParticipant() (media.Writer[media.PCM16Sample], error) {
	track, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus}, "audio", "pion")
	if err != nil {
		return nil, err
	}
	if _, err = r.room.LocalParticipant.PublishTrack(track, &lksdk.TrackPublicationOptions{
		Name: r.identity,
	}); err != nil {
		return nil, err
	}
	ow := media.FromSampleWriter[opus.Sample](track, sampleDur)
	pw, err := opus.Encode(ow, sampleRate, channels)
	if err != nil {
		return nil, err
	}
	return pw, nil
}

func (r *Room) NewTrack() *Track {
	inp := r.mix.AddInput()
	return &Track{room: r, inp: inp}
}

type Track struct {
	room *Room
	inp  *mixer.Input
}

func (t *Track) Close() error {
	t.room.mix.RemoveInput(t.inp)
	return nil
}

func (t *Track) PlayAudio(ctx context.Context, frames []media.PCM16Sample) {
	_ = media.PlayAudio[media.PCM16Sample](ctx, t, sampleDur, frames)
}

func (t *Track) WriteSample(pcm media.PCM16Sample) error {
	return t.inp.WriteSample(pcm)
}
