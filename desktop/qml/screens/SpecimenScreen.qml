import QtQuick
import FeedVideo.Core 1.0
import "../components"

// A living specimen of the component set. Not part of the product navigation:
// press F2 to open it while developing, so a theme change can be eyeballed on
// every part at once.
Flickable {
    id: root

    property var router: null

    contentWidth: width
    contentHeight: column.height + 80
    clip: true

    Column {
        id: column
        x: Theme.pagePad
        y: Theme.pagePad
        width: parent.width - Theme.pagePad * 2
        spacing: 22

        PairTitle { en: "SPECIMEN"; cn: "部件样张" }

        TriStrip { width: 240 }

        // Buttons
        Column {
            spacing: 10
            Eyebrow { label: "BUTTONS" }
            Row {
                spacing: 10
                ActionButton { label: "主要操作"; variant: "primary" }
                ActionButton { label: "次要"; variant: "secondary" }
                ActionButton { label: "次要（当前）"; variant: "secondary"; active: true }
                ActionButton { label: "轻按钮"; variant: "ghost"; icon: "refresh" }
                ActionButton { label: "删除"; variant: "danger"; icon: "trash" }
            }
        }

        // Tags and chips
        Column {
            spacing: 10
            Eyebrow { label: "TAGS" }
            Row {
                spacing: 10
                NumberTag { text: "No.024" }
                NumberTag { text: "No.025"; current: true }
                FilterChip { label: "热门"; count: 12; selected: true }
                FilterChip { label: "推荐"; count: 8 }
                FilterChip { label: "最新" }
            }
        }

        // Fields and notices
        Column {
            spacing: 12
            Eyebrow { label: "FIELDS" }
            FieldRow { fields: ["2026.11.18", "6,380 次播放", "24 条评论"] }
            FieldRow { fields: ["作者：林川", "时长 03:12", "已审核"]; strong: true }
            Notice {
                width: 520
                text: "上传失败了，文件还在这台机器上，可以重试。"
                actionLabel: "重试"
            }
        }

        // Panels
        Row {
            spacing: 16
            BoardPanel {
                width: 260
                height: 150
                en: "PLAYS"
                cn: "本月播放"
                cutCorners: Qt.BottomRightCorner

                Column {
                    anchors.fill: parent
                    spacing: 4
                    Text {
                        text: "6,380"
                        color: Theme.ink
                        font.family: Theme.fontField
                        font.pixelSize: 34
                        font.weight: Font.Bold
                        font.features: ({ "tnum": 1 })
                        renderType: Text.NativeRendering
                    }
                    Text {
                        text: "比上月多 12%"
                        color: Theme.ink2
                        font.family: Theme.fontText
                        font.pixelSize: 12
                        renderType: Text.NativeRendering
                    }
                }
            }

            BoardPanel {
                width: 260
                height: 150
                en: "TODO"
                cn: "待处理"
                cutCorners: Qt.TopLeftCorner

                Rectangle {
                    anchors.fill: parent
                    color: Theme.signal
                    Text {
                        anchors.centerIn: parent
                        text: "3 条待审核"
                        color: Theme.onSignal
                        font.family: Theme.fontText
                        font.pixelSize: 14
                        font.weight: Font.Bold
                        renderType: Text.NativeRendering
                    }
                }
            }

            Item {
                width: 260
                height: 150

                Rectangle {
                    anchors.fill: parent
                    color: "transparent"
                    border.color: Theme.rule
                    border.width: 1
                }

                Hatch { anchors.fill: parent }

                Text {
                    anchors.centerIn: parent
                    text: "斜线纹"
                    color: Theme.ink
                    font.family: Theme.fontText
                    font.pixelSize: 13
                    renderType: Text.NativeRendering
                }
            }
        }

        // Rows
        Column {
            spacing: 0
            width: parent.width - 40
            Eyebrow { label: "ROWS" }

            RecordRow {
                width: parent.width
                number: "No.012"
                title: "把一台旧笔记本改成家庭服务器"
                subtitle: "林川 · 03:12"
                fields: ["1.2 万", "240"]
            }
            RecordRow {
                width: parent.width
                number: "No.011"
                title: "城市夜骑第 4 集：江边那段"
                subtitle: "阿野 · 07:41"
                fields: ["8,204", "112"]
                current: true
            }
        }

        // Progress and icons
        Column {
            spacing: 12
            Eyebrow { label: "PROGRESS" }
            SegProgress { width: 420; value: 0.62 }
            Row {
                spacing: 16
                Repeater {
                    model: ["play", "search", "bell", "bookmark", "clock", "upload", "settings", "heart", "comment", "share"]
                    delegate: Icon {
                        required property string modelData
                        name: modelData
                        size: 20
                    }
                }
            }
        }
    }
}
