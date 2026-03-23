Feature: Camera Entity

  Scenario: FromAlexa PowerController TurnOn maps to CameraRecordStart
    When Alexa sends "Alexa.PowerController" "TurnOn" to entity type "light"
    Then the Alexa domain command type is "LightTurnOn"
