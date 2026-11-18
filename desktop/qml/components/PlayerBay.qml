import QtQuick
import QtMultimedia
import FeedVideo.Core 1.0
import "../lib/Http.js" as Http

// The stage. Video always plays inside a constant ink rectangle, in both
// themes, the way a screen stays dark in a lit room.
Item {
    id: root

    property string source: ""
    property string poster: ""
    property bool autoplay: true
    property bool compact: false

    signal closeRequested()

    implicitHeight: compact ? 180 : 300

    Rectangle {
        anchors.fill: parent
        color: "#0C0C0C"
    }

    Image {
        anchors.fill: parent
        source: root.poster
        fillMode: Image.PreserveAspectFit
        asynchronous: true
        visible: player.playbackState !== MediaPlayer.PlayingState
    }

    VideoOutput {
        id: output
        anchors.fill: parent
        fillMode: VideoOutput.PreserveAspectFit
    }

    MediaPlayer {
        id: player
        source: root.source
        videoOutput: output
        audioOutput: AudioOutput {
            volume: 0.7
            muted: root.muted
        }
        onMediaStatusChanged: {
            if (mediaStatus === MediaPlayer.EndOfMedia)
                root.closeRequested()
        }
    }

    property bool muted: false

    // Poster state: one press on the stage starts it.
    Item {
        anchors.fill: parent
        visible: player.playbackState !== MediaPlayer.PlayingState

        Rectangle {
            anchors.centerIn: parent
            width: 62
            height: 62
            color: Theme.signal

            Icon {
                anchors.centerIn: parent
                name: "play"
                size: 22
                color: Theme.onSignal
                x: parent.width / 2 - 8
            }
        }

        MouseArea {
            anchors.fill: parent
            cursorShape: Qt.PointingHandCursor
            onClicked: player.play()
        }
    }

    // Controls
    Rectangle {
        id: controls
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        height: 46
        color: Theme.commandBar
        opacity: mouse.containsMouse || !player.playing ? 1 : 0.86

        Behavior on opacity {
            NumberAnimation { duration: Theme.reduceMotion ? 0 : 120; easing: Theme.ease }
        }

        HoverHandler { id: mouse }

        Row {
            anchors.left: parent.left
            anchors.leftMargin: 12
            anchors.verticalCenter: parent.verticalCenter
            spacing: 14

            Item {
                width: 20
                height: 20
                anchors.verticalCenter: parent.verticalCenter

                Icon {
                    anchors.centerIn: parent
                    name: player.playbackState === MediaPlayer.PlayingState ? "pause" : "play"
                    size: 16
                    color: Theme.commandBarText
                }

                MouseArea {
                    anchors.fill: parent
                    anchors.margins: -8
                    cursorShape: Qt.PointingHandCursor
                    onClicked: player.playbackState === MediaPlayer.PlayingState ? player.pause() : player.play()
                }
            }

            Text {
                anchors.verticalCenter: parent.verticalCenter
                text: Http.duration(player.position / 1000) + " / " + Http.duration(player.duration / 1000)
                color: "#C9C9C9"
                font.family: Theme.fontField
                font.pixelSize: 11
                font.features: ({ "tnum": 1 })
                renderType: Text.NativeRendering
            }
        }

        Row {
            anchors.right: parent.right
            anchors.rightMargin: 12
            anchors.verticalCenter: parent.verticalCenter
            spacing: 16

            Icon {
                name: root.muted ? "volume" : "volume"
                size: 15
                color: Theme.commandBarText
                opacity: root.muted ? 0.45 : 1
                anchors.verticalCenter: parent.verticalCenter

                MouseArea {
                    anchors.fill: parent
                    anchors.margins: -8
                    cursorShape: Qt.PointingHandCursor
                    onClicked: root.muted = !root.muted
                }
            }

            Icon {
                name: "fullscreen"
                size: 15
                color: Theme.commandBarText
                anchors.verticalCenter: parent.verticalCenter
            }
        }

        // Seek track: a hairline that fills with the accent.
        Item {
            id: track
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.bottom: parent.bottom
            height: 3

            Rectangle {
                anchors.fill: parent
                color: "#333333"
            }

            Rectangle {
                height: parent.height
                width: player.duration > 0 ? parent.width * (player.position / player.duration) : 0
                color: Theme.signal
            }

            MouseArea {
                anchors.fill: parent
                anchors.topMargin: -8
                anchors.bottomMargin: -4
                cursorShape: Qt.PointingHandCursor
                onClicked: function (m) {
                    if (player.duration > 0)
                        player.position = Math.round(player.duration * (m.x / width))
                }
            }
        }
    }

    onSourceChanged: {
        if (root.source !== "" && root.autoplay)
            player.play()
    }
}
