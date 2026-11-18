import QtQuick
import FeedVideo.Core 1.0

// One row of a list: number, title, secondary line, right-hand fields.
// Pressing it drops a trace bar on the left and shifts the row 6px — 200ms,
// snapping, never a ripple.
Item {
    id: root

    property string number: ""
    property string title: ""
    property string subtitle: ""
    property var fields: []
    property bool current: false
    property bool interactive: true

    signal clicked()

    implicitHeight: 62 * Theme.density
    implicitWidth: 320

    readonly property bool hot: hoverArea.containsMouse || current

    Rectangle {
        id: traceBar
        width: 4
        height: parent.height
        color: Theme.trace
        visible: root.hot
    }

    Rectangle {
        anchors.fill: parent
        color: root.hot && root.interactive ? Theme.chip : "transparent"
    }

    Row {
        id: line
        anchors.left: parent.left
        anchors.leftMargin: 16 * Theme.density + (root.hot ? 6 : 0)
        anchors.right: parent.right
        anchors.rightMargin: 16 * Theme.density
        anchors.verticalCenter: parent.verticalCenter
        spacing: 14 * Theme.density

        Behavior on anchors.leftMargin {
            NumberAnimation { duration: Theme.reduceMotion ? 0 : Theme.dRow; easing: Theme.snap }
        }

        NumberTag {
            text: root.number
            current: root.current
            anchors.verticalCenter: parent.verticalCenter
            visible: root.number !== ""
        }

        Column {
            anchors.verticalCenter: parent.verticalCenter
            spacing: 3 * Theme.density
            width: Math.max(0, root.width - 260 * Theme.density)

            Text {
                text: root.title
                color: Theme.ink
                font.family: Theme.fontText
                font.pixelSize: 15 * Theme.density
                font.weight: Font.DemiBold
                elide: Text.ElideRight
                width: parent.width
                renderType: Text.NativeRendering
            }

            Text {
                visible: root.subtitle !== ""
                text: root.subtitle
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 12 * Theme.density
                elide: Text.ElideRight
                width: parent.width
                renderType: Text.NativeRendering
            }
        }
    }

    FieldRow {
        anchors.right: parent.right
        anchors.rightMargin: 16 * Theme.density
        anchors.verticalCenter: parent.verticalCenter
        fields: root.fields
        size: 11
    }

    Rectangle {
        anchors.bottom: parent.bottom
        anchors.left: parent.left
        anchors.right: parent.right
        height: 1
        color: Theme.rule
    }

    MouseArea {
        id: hoverArea
        anchors.fill: parent
        hoverEnabled: true
        enabled: root.interactive
        cursorShape: root.interactive ? Qt.PointingHandCursor : Qt.ArrowCursor
        onClicked: root.clicked()
    }
}
