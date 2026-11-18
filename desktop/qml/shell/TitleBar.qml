import QtQuick
import FeedVideo.Core 1.0
import "../components"

// The quiet shell: an ink command bar with the product name, the current
// section, one real connection field, and window controls that only come
// forward when you reach for them.
Rectangle {
    id: root

    property string section: ""
    property string sectionEn: ""
    property int connection: Api.state
    property string accountLabel: ""

    signal accountClicked()

    height: 44
    color: Theme.commandBar

    HoverHandler { id: barHover }

    MouseArea {
        anchors.fill: parent
        acceptedButtons: Qt.LeftButton
        onPressed: function (mouse) {
            if (mouse.button === Qt.LeftButton) {
                Window.window.startSystemMove()
            }
        }
        onDoubleClicked: {
            Window.window.visibility = (Window.window.visibility === Window.Maximized)
                ? Window.Windowed : Window.Maximized
        }
    }

    Row {
        anchors.left: parent.left
        anchors.leftMargin: 14
        anchors.verticalCenter: parent.verticalCenter
        spacing: 10

        Rectangle {
            width: 9
            height: 9
            color: Theme.signal
            anchors.verticalCenter: parent.verticalCenter
        }

        Text {
            text: "FEEDVIDEO"
            color: Theme.commandBarText
            font.family: Theme.fontWide
            font.pixelSize: 13
            font.weight: Font.Bold
            font.letterSpacing: 13 * 0.14
            font.capitalization: Font.AllUppercase
            anchors.verticalCenter: parent.verticalCenter
            renderType: Text.NativeRendering
        }

        Rectangle {
            width: 1
            height: 16
            color: "#3A3A3A"
            anchors.verticalCenter: parent.verticalCenter
            visible: root.section !== ""
        }

        Text {
            visible: root.section !== ""
            text: root.section
            color: Theme.commandBarText
            font.family: Theme.fontText
            font.pixelSize: 12
            font.weight: Font.DemiBold
            anchors.verticalCenter: parent.verticalCenter
            renderType: Text.NativeRendering
        }
    }

    // One honest status field: is the gateway reachable right now?
    Row {
        anchors.right: controls.left
        anchors.rightMargin: 18
        anchors.verticalCenter: parent.verticalCenter
        spacing: 6

        Rectangle {
            width: 6
            height: 6
            color: root.connection === Api.Ready ? Theme.signal
                 : root.connection === Api.Probing ? "#8A8A8A"
                 : "#FF8A7A"
            anchors.verticalCenter: parent.verticalCenter
        }

        Text {
            text: root.connection === Api.Ready ? qsTr("服务端已连接")
                : root.connection === Api.Probing ? qsTr("连接中")
                : qsTr("未连接服务端")
            color: "#B8B8B8"
            font.family: Theme.fontText
            font.pixelSize: 11
            anchors.verticalCenter: parent.verticalCenter
            renderType: Text.NativeRendering
        }
    }

    Row {
        id: controls
        anchors.right: parent.right
        anchors.top: parent.top
        anchors.bottom: parent.bottom
        opacity: barHover.hovered || anyHover.hovered ? 1 : 0.4

        Behavior on opacity {
            NumberAnimation { duration: Theme.reduceMotion ? 0 : 120; easing: Theme.ease }
        }

        HoverHandler { id: anyHover }

        Repeater {
            model: [
                { icon: "minimize", action: "minimize" },
                { icon: "maximize", action: "maximize" },
                { icon: "close", action: "close" }
            ]

            delegate: Item {
                id: button
                required property var modelData

                width: 46
                height: root.height

                Rectangle {
                    anchors.fill: parent
                    color: Theme.signal
                    opacity: area.containsMouse ? 1 : 0
                    Behavior on opacity {
                        NumberAnimation { duration: Theme.reduceMotion ? 0 : 120; easing: Theme.ease }
                    }
                }

                Icon {
                    anchors.centerIn: parent
                    name: button.modelData.icon
                    size: 14
                    color: area.containsMouse ? Theme.onSignal : Theme.commandBarText
                }

                MouseArea {
                    id: area
                    anchors.fill: parent
                    hoverEnabled: true
                    cursorShape: Qt.ArrowCursor

                    onClicked: {
                        if (button.modelData.action === "minimize")
                            Window.window.showMinimized()
                        else if (button.modelData.action === "maximize")
                            Window.window.visibility = (Window.window.visibility === Window.Maximized)
                                ? Window.Windowed : Window.Maximized
                        else
                            Window.window.close()
                    }
                }
            }
        }
    }
}
