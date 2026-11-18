import QtQuick
import FeedVideo.Core 1.0

// Three flat cells, 3px tall: magenta / accent / mint. It marks the start of a
// screen and the line under a detail title — those two places and no others.
Row {
    id: root

    property real thickness: 3
    property bool accentMiddle: true

    spacing: 0
    height: thickness

    Rectangle {
        width: parent.width / 3
        height: root.thickness
        color: Theme.triA
    }
    Rectangle {
        width: parent.width / 3
        height: root.thickness
        color: root.accentMiddle ? Theme.signal : Theme.triB
    }
    Rectangle {
        width: parent.width / 3
        height: root.thickness
        color: Theme.triC
    }
}
