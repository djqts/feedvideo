import QtQuick
import FeedVideo.Core 1.0

// `No.024` — ink block, white digits, bottom-right corner cut. The current item
// fills with the accent instead. Only ever shows a real identifier.
CutBlock {
    id: root

    property string text: ""
    property bool current: false

    implicitWidth: label.implicitWidth + 14 + cut
    implicitHeight: label.implicitHeight + 6

    fill: current ? Theme.signal : Theme.tagBg
    cut: Theme.cutSmall
    corners: Qt.BottomRightCorner

    Text {
        id: label
        anchors.left: parent.left
        anchors.leftMargin: 7
        anchors.verticalCenter: parent.verticalCenter
        width: Math.max(0, parent.width - 7 - root.cut)
        text: root.text
        color: root.current ? Theme.onSignal : Theme.tagFg
        font.family: Theme.fontField
        font.pixelSize: 11 * Theme.density
        font.weight: Font.DemiBold
        font.letterSpacing: 11 * Theme.density * 0.06
        elide: Text.ElideRight
        renderType: Text.NativeRendering
    }
}
