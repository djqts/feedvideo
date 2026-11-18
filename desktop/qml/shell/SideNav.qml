import QtQuick
import FeedVideo.Core 1.0
import "../components"

// Vertical navigation, one cell per destination: wide English label over the
// Chinese name, the current cell filled with the accent and cut at the corner.
Rectangle {
    id: root

    property string current: "feed"
    property string accountLabel: ""
    property bool authenticated: false

    signal navigate(string name)
    signal accountClicked()
    signal themeClicked()

    width: 196
    color: Theme.paper

    readonly property var items: [
        { id: "feed", en: "FEED", cn: "推荐", icon: "play" },
        { id: "search", en: "SEARCH", cn: "搜索", icon: "search" },
        { id: "notifications", en: "NOTIFY", cn: "通知", icon: "bell" },
        { id: "collections", en: "SAVED", cn: "收藏", icon: "bookmark" },
        { id: "history", en: "HISTORY", cn: "历史", icon: "clock" },
        { id: "creator", en: "CREATOR", cn: "创作者", icon: "user" },
        { id: "publish", en: "PUBLISH", cn: "发布", icon: "upload" },
        { id: "settings", en: "CONFIG", cn: "设置", icon: "settings" }
    ]

    Rectangle {
        width: 1
        height: parent.height
        anchors.right: parent.right
        color: Theme.rule
    }

    Column {
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.top: parent.top
        anchors.topMargin: 10
        spacing: 2

        Repeater {
            model: root.items

            delegate: Item {
                id: cell
                required property var modelData

                width: root.width
                height: 50

                readonly property bool selected: root.current === modelData.id

                CutBlock {
                    anchors.fill: parent
                    anchors.margins: 6
                    visible: cell.selected
                    fill: Theme.signal
                    cut: Theme.cutSmall
                    corners: Qt.BottomRightCorner
                }

                Row {
                    anchors.left: parent.left
                    anchors.leftMargin: 22
                    anchors.verticalCenter: parent.verticalCenter
                    spacing: 10

                    Icon {
                        name: cell.modelData.icon
                        size: 15
                        color: Theme.ink
                        opacity: cell.selected ? 1 : 0.75
                        anchors.verticalCenter: parent.verticalCenter
                    }

                    Column {
                        spacing: 1

                        Text {
                            text: "// " + cell.modelData.en
                            color: Theme.ink2
                            font.family: Theme.fontWide
                            font.pixelSize: 9
                            font.weight: Font.DemiBold
                            font.letterSpacing: 9 * 0.14
                            renderType: Text.NativeRendering
                        }

                        Text {
                            text: cell.modelData.cn
                            color: Theme.ink
                            font.family: Theme.fontText
                            font.pixelSize: 14
                            font.weight: cell.selected ? Font.Bold : Font.Normal
                            renderType: Text.NativeRendering
                        }
                    }
                }

                Rectangle {
                    anchors.fill: parent
                    anchors.margins: 3
                    color: "transparent"
                    border.color: Theme.trace
                    border.width: 2
                    visible: cell.activeFocus
                }

                MouseArea {
                    anchors.fill: parent
                    hoverEnabled: true
                    cursorShape: Qt.PointingHandCursor
                    onClicked: root.navigate(cell.modelData.id)
                }
            }
        }
    }

    Column {
        anchors.left: parent.left
        anchors.right: parent.right
        anchors.bottom: parent.bottom
        anchors.margins: 16
        spacing: 10

        Rectangle {
            width: parent.width
            height: 1
            color: Theme.rule
        }

        Row {
            width: parent.width
            spacing: 8

            Text {
                text: root.authenticated ? root.accountLabel : qsTr("未登录")
                color: root.authenticated ? Theme.ink : Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 13
                font.weight: root.authenticated ? Font.DemiBold : Font.Normal
                elide: Text.ElideRight
                width: parent.width - themeButton.width - 8
                renderType: Text.NativeRendering

                MouseArea {
                    anchors.fill: parent
                    cursorShape: Qt.PointingHandCursor
                    onClicked: root.accountClicked()
                }
            }

            Item {
                id: themeButton
                width: 28
                height: 22

                Text {
                    anchors.centerIn: parent
                    text: Theme.mode === 1 ? qsTr("亮") : qsTr("暗")
                    color: Theme.ink
                    font.family: Theme.fontText
                    font.pixelSize: 12
                    font.weight: Font.DemiBold
                    renderType: Text.NativeRendering
                }

                MouseArea {
                    anchors.fill: parent
                    cursorShape: Qt.PointingHandCursor
                    onClicked: root.themeClicked()
                }
            }
        }
    }
}
