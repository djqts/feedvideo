import QtQuick
import FeedVideo.Core 1.0
import "../components"

// Signing in. The left half is ink with the wordmark; the form on the right is
// paper, because that is the half you read and type into.
Item {
    id: root

    property var router: null
    property string username: ""
    property string password: ""
    property bool busy: false
    property string errorText: ""
    property bool registerMode: false

    function submit() {
        if (username.trim() === "" || password === "") {
            errorText = qsTr("账号和密码都要填。")
            return
        }
        busy = true
        errorText = ""
        if (registerMode)
            Api.registerAccount(username, password)
        else
            Api.login(username, password)
    }

    Connections {
        target: Api

        function onResult(tag, data) {
            if (tag === "auth.login") {
                root.busy = false
                Api.fetchMe()
                if (root.router)
                    root.router.go("feed")
            } else if (tag === "auth.register") {
                root.busy = false
                root.registerMode = false
                root.errorText = qsTr("注册成功，用这个账号登录。")
            }
        }

        function onFailed(tag, status, code, message) {
            if (tag === "auth.login" || tag === "auth.register") {
                root.busy = false
                root.errorText = message
            }
        }
    }

    Row {
        anchors.fill: parent

        // Ink half
        Item {
            width: parent.width * 0.42
            height: parent.height

            Rectangle {
                anchors.fill: parent
                color: Theme.commandBar
            }

            Column {
                anchors.left: parent.left
                anchors.bottom: parent.bottom
                anchors.margins: 40
                spacing: 10

                Row {
                    spacing: 10
                    Rectangle {
                        width: 12
                        height: 12
                        color: Theme.signal
                        anchors.verticalCenter: parent.verticalCenter
                    }
                    Text {
                        text: "FEEDVIDEO"
                        color: Theme.commandBarText
                        font.family: Theme.fontWide
                        font.pixelSize: 34
                        font.weight: Font.ExtraBold
                        font.letterSpacing: 34 * 0.04
                        renderType: Text.NativeRendering
                    }
                }

                Text {
                    text: "// DESKTOP CLIENT"
                    color: "#8E8E8E"
                    font.family: Theme.fontWide
                    font.pixelSize: 10
                    font.weight: Font.DemiBold
                    font.letterSpacing: 10 * 0.14
                    renderType: Text.NativeRendering
                }

                Text {
                    text: qsTr("短视频、创作者中心和你的收藏，\n都在这个窗口里。")
                    color: "#C9C9C9"
                    font.family: Theme.fontText
                    font.pixelSize: 14
                    lineHeight: 1.7
                    renderType: Text.NativeRendering
                }
            }

            TriStrip {
                anchors.left: parent.left
                anchors.bottom: parent.bottom
                width: parent.width
            }
        }

        // Form half
        Item {
            width: parent.width * 0.58
            height: parent.height

            Column {
                anchors.centerIn: parent
                width: Math.min(parent.width - 80, 420)
                spacing: 0

                PairTitle {
                    en: root.registerMode ? "REGISTER" : "SIGN IN"
                    cn: root.registerMode ? qsTr("注册账号") : qsTr("登录")
                }

                Item { height: 10; width: 1 }

                FieldRow {
                    fields: [Api.baseUrl]
                    size: 11
                }

                Item { height: 24; width: 1 }

                Text {
                    text: qsTr("账号")
                    color: Theme.ink2
                    font.family: Theme.fontText
                    font.pixelSize: 12
                    font.weight: Font.DemiBold
                    renderType: Text.NativeRendering
                }

                Item { height: 6; width: 1 }

                Rectangle {
                    width: parent.width
                    height: 44
                    color: Theme.panel
                    border.color: userField.activeFocus ? Theme.trace : Theme.rule
                    border.width: userField.activeFocus ? 2 : 1

                    TextInput {
                        id: userField
                        anchors.fill: parent
                        anchors.leftMargin: 12
                        anchors.rightMargin: 12
                        verticalAlignment: TextInput.AlignVCenter
                        text: root.username
                        color: Theme.ink
                        font.family: Theme.fontText
                        font.pixelSize: 15
                        selectByMouse: true
                        activeFocusOnTab: true
                        selectionColor: Theme.signal
                        selectedTextColor: Theme.onSignal
                        onTextChanged: root.username = text
                        onAccepted: root.submit()
                        Keys.onPressed: function (event) {
                            if (event.key === Qt.Key_Tab) {
                                passField.forceActiveFocus()
                                event.accepted = true
                            }
                        }
                    }
                }

                Item { height: 16; width: 1 }

                Text {
                    text: qsTr("密码")
                    color: Theme.ink2
                    font.family: Theme.fontText
                    font.pixelSize: 12
                    font.weight: Font.DemiBold
                    renderType: Text.NativeRendering
                }

                Item { height: 6; width: 1 }

                Rectangle {
                    width: parent.width
                    height: 44
                    color: Theme.panel
                    border.color: passField.activeFocus ? Theme.trace : Theme.rule
                    border.width: passField.activeFocus ? 2 : 1

                    TextInput {
                        id: passField
                        anchors.fill: parent
                        anchors.leftMargin: 12
                        anchors.rightMargin: 12
                        verticalAlignment: TextInput.AlignVCenter
                        text: root.password
                        echoMode: TextInput.Password
                        color: Theme.ink
                        font.family: Theme.fontText
                        font.pixelSize: 15
                        selectByMouse: true
                        activeFocusOnTab: true
                        selectionColor: Theme.signal
                        selectedTextColor: Theme.onSignal
                        onTextChanged: root.password = text
                        onAccepted: root.submit()
                    }
                }

                Item { height: 12; width: 1 }

                Notice {
                    width: parent.width
                    visible: root.errorText !== ""
                    text: root.errorText
                }

                Item { height: 14; width: 1 }

                ActionButton {
                    width: parent.width
                    height: 46
                    label: root.busy ? qsTr("处理中…") : (root.registerMode ? qsTr("注册") : qsTr("登录"))
                    variant: "primary"
                    cornerColor: Theme.paper
                    onClicked: root.submit()
                }

                Item { height: 12; width: 1 }

                Text {
                    text: root.registerMode ? qsTr("已有账号，去登录") : qsTr("没有账号，注册一个")
                    color: Theme.ink
                    font.family: Theme.fontText
                    font.pixelSize: 13
                    font.underline: true
                    renderType: Text.NativeRendering

                    MouseArea {
                        anchors.fill: parent
                        anchors.margins: -6
                        cursorShape: Qt.PointingHandCursor
                        onClicked: {
                            root.registerMode = !root.registerMode
                            root.errorText = ""
                        }
                    }
                }
            }
        }
    }

    Component.onCompleted: userField.forceActiveFocus()

    // The screen is created once and reused, so focus has to follow navigation.
    Connections {
        target: root.router
        function onNavigated(name) {
            if (name === "login")
                userField.forceActiveFocus()
        }
    }
}
