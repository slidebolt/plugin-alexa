Feature: Siren Entity

  Scenario: FromAlexa TurnOn maps to SirenTurnOn
    When Alexa sends "Alexa.PowerController" "TurnOn" to entity type "siren"
    Then the Alexa domain command type is "SirenTurnOn"

  Scenario: FromAlexa TurnOff maps to SirenTurnOff
    When Alexa sends "Alexa.PowerController" "TurnOff" to entity type "siren"
    Then the Alexa domain command type is "SirenTurnOff"
