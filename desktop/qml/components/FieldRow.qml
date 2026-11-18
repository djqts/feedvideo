import QtQuick
import FeedVideo.Core 1.0

// `2026.11.18 │ 6,380 次播放 │ 12 评论` — metadata is separated by vertical
// rules, never by middle dots, and every number is real.
Row {
    id: root

    property var fields: []          // array of strings
    property color tone: Theme.ink2
    property real size: 12
    property bool strong: false

    spacing: 8

    Repeater {
        model: root.fields

        delegate: Row {
            spacing: 8

            required property int index
            required property string modelData

            Text {
                visible: index > 0
                text: "│"
                color: Theme.rule
                font.pixelSize: root.size * Theme.density
                renderType: Text.NativeRendering
            }

            Text {
                text: modelData
                color: root.strong ? Theme.ink : root.tone
                font.family: Theme.fontField
                font.pixelSize: root.size * Theme.density
                font.weight: root.strong ? Font.DemiBold : Font.Normal
                font.features: ({ "tnum": 1 })
                renderType: Text.NativeRendering
            }
        }
    }
}
