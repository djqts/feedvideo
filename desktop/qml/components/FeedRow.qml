import QtQuick
import FeedVideo.Core 1.0
import "../components"
import "../lib/Http.js" as Http

// One line of the archive: number, cover, title, author, figures, duration.
// Pressing it opens the preview bay in place — the list never turns into a wall
// of cards, and the video never takes over the whole window.
Item {
    id: root

    property var video: null
    property string number: ""
    property bool expanded: false

    signal activated()
    signal detailRequested(string id)

    readonly property var info: video || ({})
    readonly property string cover: Http.mediaUrl(root.info.cover_url || root.info.cover || "", Api.mediaBase, Api.baseUrl)
    readonly property string playable: Http.mediaUrl(root.info.transcoded_url || root.info.video_url || root.info.play_url || "", Api.mediaBase, Api.baseUrl)
    readonly property string title: Http.firstOf(root.info.title, qsTr("未命名"))
    readonly property string author: Http.author(root.info)
    readonly property real seconds: Http.num(root.info.duration || root.info.duration_seconds)

    readonly property real baseHeight: 78 * Theme.density
    readonly property real bayHeight: Math.max(200, Math.min(360, width * 0.38))
    readonly property bool hot: hover.containsMouse || expanded

    height: baseHeight + (expanded ? bayHeight : 0)

    Behavior on height {
        NumberAnimation { duration: Theme.reduceMotion ? 0 : Theme.dCome; easing: Theme.snap }
    }

    Rectangle {
        anchors.fill: parent
        color: root.hot ? Theme.chip : "transparent"
    }

    Rectangle {
        width: 4
        height: parent.baseHeight
        color: Theme.trace
        visible: root.hot
    }

    Row {
        id: line
        anchors.top: parent.top
        height: root.baseHeight
        anchors.left: parent.left
        anchors.leftMargin: 16 + (root.hot ? 6 : 0)
        anchors.right: parent.right
        anchors.rightMargin: 16
        spacing: 14

        Behavior on anchors.leftMargin {
            NumberAnimation { duration: Theme.reduceMotion ? 0 : Theme.dRow; easing: Theme.snap }
        }

        NumberTag {
            text: root.number
            current: root.expanded
            anchors.verticalCenter: parent.verticalCenter
        }

        Item {
            width: 104
            height: 58
            anchors.verticalCenter: parent.verticalCenter
            clip: true

            Hatch {
                anchors.fill: parent
                visible: coverImage.status !== Image.Ready
            }

            Image {
                id: coverImage
                anchors.fill: parent
                source: root.cover
                fillMode: Image.PreserveAspectCrop
                asynchronous: true
                cache: true
            }

            Rectangle {
                anchors.fill: parent
                border.color: Theme.rule
                color: "transparent"
            }

            Text {
                anchors.left: parent.left
                anchors.bottom: parent.bottom
                anchors.margins: 4
                text: Http.duration(root.seconds)
                visible: root.seconds > 0
                color: "#FFFFFF"
                font.family: Theme.fontField
                font.pixelSize: 10
                font.weight: Font.DemiBold
                font.features: ({ "tnum": 1 })
                renderType: Text.NativeRendering

                Rectangle {
                    anchors.fill: parent
                    anchors.margins: -2
                    color: "#99000000"
                    z: -1
                }
            }
        }

        Column {
            anchors.verticalCenter: parent.verticalCenter
            width: Math.max(0, root.width - 330 * Theme.density)
            spacing: 6

            Text {
                text: root.title
                color: Theme.ink
                font.family: Theme.fontText
                font.pixelSize: 15
                font.weight: Font.DemiBold
                elide: Text.ElideRight
                width: parent.width
                renderType: Text.NativeRendering
            }

            FieldRow {
                fields: [
                    root.author,
                    Http.count(Http.plays(root.info)) + qsTr(" 次播放"),
                    Http.count(Http.likes(root.info)) + qsTr(" 赞"),
                    Http.date(root.info.created_at)
                ]
                size: 11
            }
        }
    }

    Icon {
        anchors.right: parent.right
        anchors.rightMargin: 18
        anchors.top: parent.top
        anchors.topMargin: root.baseHeight / 2 - 8
        name: root.expanded ? "chevron-down" : "play"
        size: 15
        color: root.expanded ? Theme.ink : Theme.ink2
        opacity: root.hot ? 1 : 0.5
    }

    // The bay itself.
    Item {
        id: bay
        x: 16 + 6
        y: root.baseHeight
        width: parent.width - 32 - 6
        height: root.expanded ? root.bayHeight - 22 : 0
        clip: true
        visible: height > 2

        PlayerBay {
            anchors.fill: parent
            source: root.expanded ? root.playable : ""
            poster: root.cover
            compact: true
            onCloseRequested: root.activated()
        }

        // The cut corner marks the bay as something you can dismiss.
        Row {
            anchors.right: parent.right
            anchors.top: parent.top
            anchors.margins: 8
            spacing: 14

            Text {
                text: qsTr("打开详情")
                color: Theme.ink
                font.family: Theme.fontText
                font.pixelSize: 11
                font.weight: Font.DemiBold
                font.underline: true
                renderType: Text.NativeRendering

                MouseArea {
                    anchors.fill: parent
                    anchors.margins: -6
                    cursorShape: Qt.PointingHandCursor
                    onClicked: root.detailRequested(String(root.info.id || ""))
                }
            }

            Text {
                text: qsTr("收起 ▴")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 11
                renderType: Text.NativeRendering

                MouseArea {
                    anchors.fill: parent
                    anchors.margins: -6
                    cursorShape: Qt.PointingHandCursor
                    onClicked: root.activated()
                }
            }
        }
    }

    Rectangle {
        anchors.bottom: parent.bottom
        anchors.left: parent.left
        anchors.right: parent.right
        height: 1
        color: Theme.rule
    }

    MouseArea {
        id: hover
        height: root.baseHeight
        width: parent.width
        hoverEnabled: true
        cursorShape: Qt.PointingHandCursor
        onClicked: root.activated()
        onDoubleClicked: root.detailRequested(String(root.info.id || ""))
    }
}
