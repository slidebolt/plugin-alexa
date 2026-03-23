Feature: Media Player Entity

  Scenario: FromAlexa PlaybackController Play
    When Alexa sends "Alexa.PlaybackController" "Play" to entity type "media_player"
    Then the Alexa domain command type is "MediaPlay"

  Scenario: FromAlexa PlaybackController Pause
    When Alexa sends "Alexa.PlaybackController" "Pause" to entity type "media_player"
    Then the Alexa domain command type is "MediaPause"

  Scenario: FromAlexa PlaybackController Stop
    When Alexa sends "Alexa.PlaybackController" "Stop" to entity type "media_player"
    Then the Alexa domain command type is "MediaStop"

  Scenario: FromAlexa PlaybackController Next
    When Alexa sends "Alexa.PlaybackController" "Next" to entity type "media_player"
    Then the Alexa domain command type is "MediaNextTrack"

  Scenario: FromAlexa Speaker SetVolume
    When Alexa sends "Alexa.Speaker" "SetVolume" to entity type "media_player" with payload '{"volume": 50}'
    Then the Alexa domain command type is "MediaSetVolume"

  Scenario: FromAlexa InputController SelectInput
    When Alexa sends "Alexa.InputController" "SelectInput" to entity type "media_player" with payload '{"input": "HDMI1"}'
    Then the Alexa domain command type is "MediaSelectSource"
