import QtQuick
import FeedVideo.Core 1.0

// Square switch. On = accent track with an ink block; off = chip track with a
// paper block. Never a rounded pill.
Item {
    id: root

    property bool checked: false
    property string label: ""

    signal toggled()

    implicitWidth: 46
    implicitHeight: 24

    Rectangle {
        anchors.fill: parent
        color: root.checked ? Theme.signal : Theme.chip
        border.color: root.checked ? Theme.signal : Theme.rule
        border.width: 1
    }

    Rectangle {
        width: 18
        height: 16
        y: 4
        x: root.checked ? parent.width - width - 4 : 4
        color: root.checked ? Theme.ink : Theme.panel

        Behavior on x {
            NumberAnimation { duration: Theme.reduceMotion ? 0 : Theme.dPress; easing: Theme.ease }
        }
    }

    Rectangle {
        anchors.fill: parent
        anchors.margins: -3
        color: "transparent"
        border.color: Theme.trace
        border.width: 2
        visible: root.activeFocus
    }

    MouseArea {
        anchors.fill: parent
        hoverEnabled: true
        cursorShape: Qt.PointingHandCursor
        onClicked: {
            root.checked = !root.checked
            root.toggled()
        }
    }
}
