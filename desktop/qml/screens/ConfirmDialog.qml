import QtQuick
import QtQuick.Controls
import FeedVideo.Core 1.0
import "../components"

// A board-shaped dialog over the whole window: white panel, heavy ink tab, the
// corner facing the outside cut away, scrim without blur. The confirming button
// sits on the right, and deleting is the only thing allowed to be red.
Popup {
    id: root

    property string eyebrow: "CONFIRM"
    property string question: ""
    property string detail: ""
    property string confirmLabel: qsTr("确认")
    property string cancelLabel: qsTr("取消")
    property bool danger: false
    // Optional single choice: [{ label: "垃圾广告", value: "spam" }, ...]
    property var options: []
    property string selectedValue: ""

    signal confirmed()
    signal canceled()

    readonly property bool blocked: options.length > 0 && selectedValue === ""

    function ask(settings) {
        eyebrow = settings.eyebrow !== undefined ? settings.eyebrow : "CONFIRM"
        question = settings.question || ""
        detail = settings.detail || ""
        confirmLabel = settings.confirmLabel || qsTr("确认")
        cancelLabel = settings.cancelLabel || qsTr("取消")
        danger = !!settings.danger
        options = settings.options || []
        selectedValue = ""
        open()
    }

    modal: true
    focus: true
    dim: true
    closePolicy: Popup.CloseOnEscape | Popup.CloseOnPressOutside
    padding: 0
    anchors.centerIn: Overlay.overlay
    width: Math.min(470, Overlay.overlay ? Overlay.overlay.width - 80 : 470)
    height: body.implicitHeight + 40

    Overlay.modal: Rectangle {
        anchors.fill: parent
        color: Theme.scrim
    }

    background: CutBlock {
        fill: Theme.panel
        strokeColor: Theme.ink
        strokeWidth: 1
        cut: Theme.cutPanel
        corners: Qt.BottomRightCorner
    }

    contentItem: Item {
        implicitHeight: body.implicitHeight + 40

        Column {
            id: body
            anchors.left: parent.left
            anchors.right: parent.right
            anchors.top: parent.top
            anchors.margins: 20
            spacing: 10

            Eyebrow { label: root.eyebrow }

            Text {
                width: parent.width
                text: root.question
                color: Theme.ink
                font.family: Theme.fontText
                font.pixelSize: 17
                font.weight: Font.Bold
                wrapMode: Text.WordWrap
                renderType: Text.NativeRendering
            }

            Text {
                width: parent.width
                visible: root.detail !== ""
                text: root.detail
                color: Theme.ink2
                font.family: Theme.fontText
                font.pixelSize: 12
                lineHeight: 1.6
                wrapMode: Text.WordWrap
                renderType: Text.NativeRendering
            }

            Flow {
                width: parent.width
                spacing: 8
                visible: root.options.length > 0

                Repeater {
                    model: root.options

                    delegate: FilterChip {
                        required property var modelData
                        label: modelData.label
                        selected: root.selectedValue === modelData.value
                        onToggled: root.selectedValue = modelData.value
                    }
                }
            }

            Item { width: 1; height: 6 }

            // A Column positions its children, so the right-aligned button row
            // gets a wrapper with a real height instead of anchors on itself.
            Item {
                width: parent.width
                height: buttons.height

                Row {
                    id: buttons
                    anchors.right: parent.right
                    spacing: 10

                    ActionButton {
                        label: root.cancelLabel
                        variant: "ghost"
                        cornerColor: Theme.panel
                        onClicked: {
                            root.close()
                            root.canceled()
                        }
                    }

                    ActionButton {
                        label: root.confirmLabel
                        variant: root.danger ? "danger" : "primary"
                        cornerColor: Theme.panel
                        enabled: !root.blocked
                        onClicked: {
                            root.close()
                            root.confirmed()
                        }
                    }
                }
            }
        }
    }
}
