# Reviewing the Actions verifier

The verifier compares an original Actions API request with the ordered calls
from a prepared response. Use the README's result and limitations section
when reviewing what a passing result means.

Review trust boundaries and behavior in addition to the mechanical Go and
workflow checks. The live harness is opt-in test code, not part of verification.
