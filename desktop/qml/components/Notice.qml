import QtQuick
import FeedVideo.Core 1.0

// Something to look at: a white block with a hairline and a yellow flag in
// front. Errors use this too — red is reserved for deleting things.
Rectangle {
    id: root

    property string text: ""
    property string actionLabel: ""

    signal actionClicked()

    implicitHeight: body.implicitHeight + 22 * Theme.density
    implicitWidth: body.implicitWidth + 44 * Theme.density
    color: Theme.panel
    border.color: Theme.rule
    border.width: 1

    Row {
        id: body
        anchors.left: parent.left
        anchors.leftMargin: 16 * Theme.density
        anchors.verticalCenter: parent.verticalCenter
        spacing: 12 * Theme.density

        Rectangle {
            width: 4
            height: 16 * Theme.density
            color: Theme.signal
            anchors.verticalCenter: parent.verticalCenter
        }

        Text {
            text: root.text
            color: Theme.ink
            font.family: Theme.fontText
            font.pixelSize: 13 * Theme.density
            font.weight: Font.Normal
            wrapMode: Text.WordWrap
            width: Math.min(implicitWidth, 520 * Theme.density)
            visible: root.actionLabel === ""
            renderType: Text.NativeRendering
        }

        Text {
            visible: root.actionLabel !== ""
            text: root.text + "  "
            color: Theme.ink
            font.family: Theme.fontText
            font.pixelSize: 13 * Theme.density
            renderType: Text.NativeRendering
        }

        Text {
            visible: root.actionLabel !== ""
            text: root.actionLabel
            color: Theme.ink
            font.family: Theme.fontText
            font.pixelSize: 13 * Theme.density
            font.weight: Font.DemiBold
            font.underline: true
            renderType: Text.NativeRendering

            MouseArea {
                anchors.fill: parent
                anchors.margins: -4
                cursorShape: Qt.PointingHandCursor
                onClicked: root.actionClicked()
            }
        }
    }
}
