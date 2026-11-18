import QtQuick
import FeedVideo.Core 1.0

// Segmented progress: 24 cells, lit cells use the accent, plus a wide-digit
// readout. Never a spinning indicator.
Row {
    id: root

    property real value: 0          // 0..1
    property int cells: 24
    property bool showReadout: true
    property string caption: ""

    spacing: 2

    Repeater {
        model: root.cells

        delegate: Rectangle {
            required property int index
            height: 10 * Theme.density
            width: Math.max(2, (root.width - (root.cells - 1) * 2) / root.cells)
            color: (index + 1) / root.cells <= root.value ? Theme.signal : Theme.chip
        }
    }
}
