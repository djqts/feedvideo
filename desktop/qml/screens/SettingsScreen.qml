import QtQuick
import QtQuick.Controls
import FeedVideo.Core 1.0
import "../components"

// Where the client talks to, how it looks, and how it moves. Every control here
// changes something real and remembers it.
Item {
    id: root

    property var router: null
    property string address: Api.baseUrl
    property int preference: Theme.preference

    readonly property var stateNames: ({
        0: qsTr("尚未连接"),
        1: qsTr("连接中"),
        2: qsTr("已连接"),
        3: qsTr("连不上"),
        4: qsTr("需要登录")
    })

    Flickable {
        anchors.fill: parent
        anchors.margins: Theme.pagePad
        contentWidth: width
        contentHeight: column.height
        clip: true

        Column {
            id: column
            width: parent.width
            spacing: 0

            PairTitle { en: "CONFIG"; cn: "设置" }

            Item { height: 16; width: 1 }

            TriStrip { width: 160 }

            Item { height: 26; width: 1 }

            // Server
            Eyebrow { label: "SERVER" }

            Item { height: 8; width: 1 }

            Text {
                text: qsTr("服务端地址")
                color: Theme.ink
                font.family: Theme.fontText
                font.pixelSize: 14
                font.weight: Font.DemiBold
                renderType: Text.NativeRendering
            }

            Item { height: 6; width: 1 }

            Rectangle {
                width: Math.min(parent.width, 520)
                height: 42
                color: Theme.panel
                border.color: addressField.activeFocus ? Theme.trace : Theme.rule
                border.width: addressField.activeFocus ? 2 : 1

                TextInput {
                    id: addressField
                    anchors.fill: parent
                    anchors.leftMargin: 12
                    anchors.rightMargin: 12
                    verticalAlignment: TextInput.AlignVCenter
                    text: root.address
                    color: Theme.ink
                    font.family: Theme.fontField
                    font.pixelSize: 13
                    selectByMouse: true
                    selectionColor: Theme.signal
                    selectedTextColor: Theme.onSignal
                    onTextChanged: root.address = text
                }
            }

            Item { height: 6; width: 1 }

            FieldRow {
                fields: [
                    qsTr("状态：") + root.stateNames[Api.state],
                    Api.lastError !== "" ? Api.lastError : qsTr("无错误")
                ]
                size: 11
            }

            Item { height: 10; width: 1 }

            Row {
                spacing: 10

                ActionButton {
                    label: qsTr("保存并重连")
                    variant: "primary"
                    cornerColor: Theme.paper
                    onClicked: {
                        Api.baseUrl = root.address.trim()
                        Api.probe()
                    }
                }

                ActionButton {
                    label: qsTr("现在检测")
                    variant: "ghost"
                    cornerColor: Theme.paper
                    onClicked: Api.probe()
                }
            }

            Item { height: 28; width: 1 }
            Rectangle { width: parent.width; height: 1; color: Theme.rule }
            Item { height: 20; width: 1 }

            // Appearance
            Eyebrow { label: "APPEARANCE" }

            Item { height: 10; width: 1 }

            Text {
                text: qsTr("外观")
                color: Theme.ink
                font.family: Theme.fontText
                font.pixelSize: 14
                font.weight: Font.DemiBold
                renderType: Text.NativeRendering
            }

            Item { height: 4; width: 1 }

            Text {
                text: qsTr("看视频时自动变暗，读文字和填表时保持明亮。")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 12
                renderType: Text.NativeRendering
            }

            Item { height: 8; width: 1 }

            Row {
                spacing: 8

                FilterChip {
                    label: qsTr("跟随屏幕")
                    selected: root.preference === 0
                    onToggled: root.setPreference(0)
                }
                FilterChip {
                    label: qsTr("始终亮")
                    selected: root.preference === 1
                    onToggled: root.setPreference(1)
                }
                FilterChip {
                    label: qsTr("始终暗")
                    selected: root.preference === 2
                    onToggled: root.setPreference(2)
                }
            }

            Item { height: 22; width: 1 }

            // Motion
            Eyebrow { label: "MOTION" }

            Item { height: 10; width: 1 }

            Row {
                width: parent.width
                spacing: 16

                Column {
                    spacing: 4
                    width: parent.width - motionSwitch.width - 16

                    Text {
                        text: qsTr("减少动态效果")
                        color: Theme.ink
                        font.family: Theme.fontText
                        font.pixelSize: 14
                        font.weight: Font.DemiBold
                        renderType: Text.NativeRendering
                    }

                    Text {
                        text: qsTr("关掉开页和切换的动效，只留下必要的状态反馈。")
                        color: Theme.ink2
                        font.family: Theme.fontText
                        font.pixelSize: 12
                        renderType: Text.NativeRendering
                    }
                }

                ToggleSquare {
                    id: motionSwitch
                    anchors.verticalCenter: parent.verticalCenter
                    checked: Theme.reduceMotion
                    onToggled: Theme.reduceMotion = !Theme.reduceMotion
                }
            }

            Item { height: 28; width: 1 }
            Rectangle { width: parent.width; height: 1; color: Theme.rule }
            Item { height: 20; width: 1 }

            // About
            Eyebrow { label: "ABOUT" }

            Item { height: 8; width: 1 }

            FieldRow {
                fields: [
                    "FeedVideo Desktop 0.1.0",
                    "Qt " + Theme.qtVersion,
                    qsTr("字体 Saira + FeedVideo Sans（OFL 1.1）")
                ]
                size: 11
            }

            Item { height: 12; width: 1 }

            Text {
                text: qsTr("数据来自你连的那个服务端；这里不保存任何视频文件。")
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 12
                renderType: Text.NativeRendering
            }

            Item { height: 6; width: 1 }

            Text {
                width: parent.width
                text: qsTr("登录状态保存在：") + Api.sessionStore
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 11
                wrapMode: Text.WrapAnywhere
                renderType: Text.NativeRendering
            }

            Item { height: 40; width: 1 }
        }
    }

    function setPreference(value) {
        root.preference = value
        Theme.preference = value
    }
}
