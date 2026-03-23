Feature: Humidifier Entity

  Scenario: FromAlexa TurnOn maps to HumidifierTurnOn
    When Alexa sends "Alexa.PowerController" "TurnOn" to entity type "humidifier"
    Then the Alexa domain command type is "HumidifierTurnOn"

  Scenario: FromAlexa TurnOff maps to HumidifierTurnOff
    When Alexa sends "Alexa.PowerController" "TurnOff" to entity type "humidifier"
    Then the Alexa domain command type is "HumidifierTurnOff"

  Scenario: FromAlexa RangeController maps to HumidifierSetHumidity
    When Alexa sends "Alexa.RangeController" "SetRangeValue" to entity type "humidifier" with payload '{"_instance": "Humidifier.Humidity", "rangeValue": 60}'
    Then the Alexa domain command type is "HumidifierSetHumidity"
