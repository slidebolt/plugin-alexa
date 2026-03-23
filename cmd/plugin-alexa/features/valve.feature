Feature: Valve Entity

  Scenario: FromAlexa TurnOn maps to ValveOpen
    When Alexa sends "Alexa.PowerController" "TurnOn" to entity type "valve"
    Then the Alexa domain command type is "ValveOpen"

  Scenario: FromAlexa TurnOff maps to ValveClose
    When Alexa sends "Alexa.PowerController" "TurnOff" to entity type "valve"
    Then the Alexa domain command type is "ValveClose"

  Scenario: FromAlexa RangeController maps to ValveSetPosition
    When Alexa sends "Alexa.RangeController" "SetRangeValue" to entity type "valve" with payload '{"_instance": "Valve.Position", "rangeValue": 50}'
    Then the Alexa domain command type is "ValveSetPosition"
