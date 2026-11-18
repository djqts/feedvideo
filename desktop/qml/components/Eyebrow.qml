import QtQuick
import FeedVideo.Core 1.0

// `// RECORDS` — the small wide label that names a section. Always paired with
// the bold Chinese line underneath, never used on its own inside body copy.
Text {
    id: root

    property string label: ""
    property color tone: Theme.ink2
    property real size: 11

    text: "// " + label
    color: tone
    font.family: Theme.fontWide
    font.pixelSize: size * Theme.density
    font.weight: Font.DemiBold
    font.letterSpacing: size * Theme.density * 0.14
    font.capitalization: Font.AllUppercase
    renderType: Text.NativeRendering
}
