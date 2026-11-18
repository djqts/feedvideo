import QtQuick
import FeedVideo.Core 1.0

// A panel on the board: white, one hairline border, a heavy ink tab on the top
// edge, and the corner facing the centre cut away.
Item {
    id: root

    property string en: ""
    property string cn: ""
    property real tabWidth: 88
    property int cutCorners: Qt.BottomRightCorner
    property color fill: Theme.panel

    default property alias content: body.data

    CutBlock {
        anchors.fill: parent
        fill: root.fill
        strokeColor: Theme.rule
        strokeWidth: 1
        cut: Theme.cutPanel
        corners: root.cutCorners
    }

    Rectangle {
        id: tab
        width: root.tabWidth * Theme.density
        height: 5
        color: Theme.ink
    }

    Column {
        id: header
        anchors.left: parent.left
        anchors.leftMargin: 18 * Theme.density
        anchors.top: parent.top
        anchors.topMargin: 18 * Theme.density
        spacing: 2

        Eyebrow { label: root.en; visible: root.en !== "" }
        Text {
            text: root.cn
            visible: root.cn !== ""
            color: Theme.ink
            font.family: Theme.fontText
            font.pixelSize: 16 * Theme.density
            font.weight: Font.Bold
            renderType: Text.NativeRendering
        }
    }

    Item {
        id: body
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        anchors.top: header.bottom
        anchors.margins: 18 * Theme.density
        anchors.topMargin: 12 * Theme.density
    }
}
