import QtQuick
import FeedVideo.Core 1.0

// State of a record: a solid frame means "live", a quiet frame means "still
// being worked on", and an accent block means "look at this". Red is never
// used here — it belongs to deleting things only.
CutBlock {
    id: root

    property string label: ""
    property string kind: "live"   // live | background | attention

    implicitWidth: caption.implicitWidth + 16 + cut
    implicitHeight: 22 * Theme.density
    cut: Theme.cutSmall
    corners: Qt.BottomRightCorner
    fill: kind === "attention" ? Theme.signal : "transparent"
    strokeColor: kind === "background" ? Theme.rule : Theme.ink
    strokeWidth: 1

    Text {
        id: caption
        anchors.left: parent.left
        anchors.leftMargin: 8
        anchors.verticalCenter: parent.verticalCenter
        text: root.label
        color: root.kind === "attention" ? Theme.onSignal
             : root.kind === "background" ? Theme.ink2
             : Theme.ink
        font.family: Theme.fontText
        font.pixelSize: 11 * Theme.density
        font.weight: Font.DemiBold
        renderType: Text.NativeRendering
    }
}
