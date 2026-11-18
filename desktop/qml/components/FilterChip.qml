import QtQuick
import FeedVideo.Core 1.0

// A filter you can press. Selected means the accent fills the block and the
// count stays readable on top of it.
CutBlock {
    id: root

    property string label: ""
    property int count: -1
    property bool selected: false

    signal toggled()

    implicitHeight: 26 * Theme.density
    implicitWidth: caption.implicitWidth + 18 * Theme.density + cut
    cut: Theme.cutSmall
    corners: Qt.BottomRightCorner
    fill: selected ? Theme.signal : Theme.chip

    Row {
        id: caption
        anchors.left: parent.left
        anchors.leftMargin: 9 * Theme.density
        anchors.verticalCenter: parent.verticalCenter
        spacing: 6 * Theme.density

        Text {
            text: root.label
            color: root.selected ? Theme.onSignal : Theme.ink
            font.family: Theme.fontText
            font.pixelSize: 13 * Theme.density
            font.weight: root.selected ? Font.DemiBold : Font.Normal
            renderType: Text.NativeRendering
        }

        Text {
            visible: root.count >= 0
            text: root.count
            color: root.selected ? Theme.onSignal : Theme.ink2
            font.family: Theme.fontField
            font.pixelSize: 11 * Theme.density
            font.weight: Font.DemiBold
            font.features: ({ "tnum": 1 })
            renderType: Text.NativeRendering
        }
    }

    Rectangle {
        anchors.fill: parent
        anchors.margins: 2
        color: "transparent"
        border.color: Theme.trace
        border.width: 2
        visible: root.activeFocus
    }

    MouseArea {
        anchors.fill: parent
        hoverEnabled: true
        cursorShape: Qt.PointingHandCursor
        onClicked: root.toggled()
    }
}
