import QtQuick
import FeedVideo.Core 1.0

// Wide upper-case Latin over bold Chinese: the pairing that names every
// section and screen in this product.
Column {
    id: root

    property string en: ""
    property string cn: ""
    property real cnSize: 30
    property color enTone: Theme.ink2
    property color cnTone: Theme.ink
    property real spacing_: 2

    spacing: spacing_ * Theme.density

    Eyebrow {
        label: root.en
        tone: root.enTone
    }

    Text {
        text: root.cn
        color: root.cnTone
        font.family: Theme.fontText
        font.pixelSize: root.cnSize * Theme.density
        font.weight: Font.Bold
        renderType: Text.NativeRendering
    }
}
