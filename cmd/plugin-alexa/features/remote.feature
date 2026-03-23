Feature: Remote Entity

  Scenario: FromAlexa TurnOn maps to RemoteTurnOn
    When Alexa sends "Alexa.PowerController" "TurnOn" to entity type "remote"
    Then the Alexa domain command type is "RemoteTurnOn"

  Scenario: FromAlexa TurnOff maps to RemoteTurnOff
    When Alexa sends "Alexa.PowerController" "TurnOff" to entity type "remote"
    Then the Alexa domain command type is "RemoteTurnOff"
