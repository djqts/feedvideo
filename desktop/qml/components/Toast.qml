import QtQuick
import FeedVideo.Core 1.0

// A dark bar with an accent edge, one sentence, then a hard cut two seconds
// later. No fade, no slide.
Rectangle {
    id: root

    property string text: ""

    width: line.implicitWidth + 40
    height: 40 * Theme.density
    color: Theme.commandBar
    visible: opacity > 0
    opacity: 0

    Row {
        id: line
        anchors.left: parent.left
        anchors.leftMargin: 14 * Theme.density
        anchors.verticalCenter: parent.verticalCenter
        spacing: 12 * Theme.density

        Rectangle {
            width: 6
            height: 18 * Theme.density
            color: Theme.signal
            anchors.verticalCenter: parent.verticalCenter
        }

        Text {
            id: message
            text: root.text
            color: Theme.commandBarText
            font.family: Theme.fontText
            font.pixelSize: 13 * Theme.density
            renderType: Text.NativeRendering
        }
    }

    function show(msg) {
        root.text = msg
        root.opacity = 1
        hideTimer.restart()
    }

    Timer {
        id: hideTimer
        interval: 2000
        onTriggered: root.opacity = 0
    }
}
