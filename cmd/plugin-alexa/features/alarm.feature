Feature: Alarm Entity

  Scenario: ToAlexa endpoint has SecurityPanelController
    Given a light entity "test.dev1.dummy" named "Dummy" with power off
    When I translate "test.dev1.dummy" to Alexa endpoint
    Then the Alexa endpoint has capability "Alexa.EndpointHealth"

  Scenario: FromAlexa Arm maps to AlarmArmAway
    When Alexa sends "Alexa.SecurityPanelController" "Arm" to entity type "alarm" with payload '{"armState": "ARMED_AWAY"}'
    Then the Alexa domain command type is "AlarmArmAway"

  Scenario: FromAlexa Disarm maps to AlarmDisarm
    When Alexa sends "Alexa.SecurityPanelController" "Disarm" to entity type "alarm"
    Then the Alexa domain command type is "AlarmDisarm"
