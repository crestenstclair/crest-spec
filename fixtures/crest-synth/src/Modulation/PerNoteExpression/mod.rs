#[derive(Debug, Clone, Copy)]
pub struct PerNoteExpression {
    pub bend_x: f64,
    pub timbre_y: f64,
    pub pressure_z: f64,
}

#[derive(Debug, Clone, PartialEq)]
pub struct PerNoteExpressionError {
    pub field: &'static str,
    pub value: f64,
}

impl std::fmt::Display for PerNoteExpressionError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(
            f,
            "PerNoteExpression: field `{}` value {} is out of range 0.0..=1.0",
            self.field, self.value
        )
    }
}

impl std::error::Error for PerNoteExpressionError {}

impl PerNoteExpression {
    pub fn new(bend_x: f64, timbre_y: f64, pressure_z: f64) -> Result<Self, PerNoteExpressionError> {
        if !(0.0..=1.0).contains(&bend_x) {
            return Err(PerNoteExpressionError { field: "bend_x", value: bend_x });
        }
        if !(0.0..=1.0).contains(&timbre_y) {
            return Err(PerNoteExpressionError { field: "timbre_y", value: timbre_y });
        }
        if !(0.0..=1.0).contains(&pressure_z) {
            return Err(PerNoteExpressionError { field: "pressure_z", value: pressure_z });
        }
        Ok(Self { bend_x, timbre_y, pressure_z })
    }
}

impl Default for PerNoteExpression {
    fn default() -> Self {
        Self {
            bend_x: 0.5,
            timbre_y: 0.0,
            pressure_z: 0.0,
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn default_has_center_bend_and_zero_timbre_pressure() {
        let expr = PerNoteExpression::default();
        assert_eq!(expr.bend_x, 0.5);
        assert_eq!(expr.timbre_y, 0.0);
        assert_eq!(expr.pressure_z, 0.0);
    }

    #[test]
    fn new_with_valid_values_succeeds() {
        let expr = PerNoteExpression::new(0.5, 0.3, 0.8).unwrap();
        assert_eq!(expr.bend_x, 0.5);
        assert_eq!(expr.timbre_y, 0.3);
        assert_eq!(expr.pressure_z, 0.8);
    }

    #[test]
    fn new_with_boundary_values_succeeds() {
        assert!(PerNoteExpression::new(0.0, 0.0, 0.0).is_ok());
        assert!(PerNoteExpression::new(1.0, 1.0, 1.0).is_ok());
    }

    #[test]
    fn new_with_bend_x_below_zero_returns_error() {
        let result = PerNoteExpression::new(-0.1, 0.5, 0.5);
        assert!(result.is_err());
        let err = result.unwrap_err();
        assert_eq!(err.field, "bend_x");
    }

    #[test]
    fn new_with_bend_x_above_one_returns_error() {
        let result = PerNoteExpression::new(1.1, 0.5, 0.5);
        assert!(result.is_err());
        let err = result.unwrap_err();
        assert_eq!(err.field, "bend_x");
    }

    #[test]
    fn new_with_timbre_y_out_of_range_returns_error() {
        let result = PerNoteExpression::new(0.5, -0.01, 0.5);
        assert!(result.is_err());
        assert_eq!(result.unwrap_err().field, "timbre_y");

        let result = PerNoteExpression::new(0.5, 1.01, 0.5);
        assert!(result.is_err());
        assert_eq!(result.unwrap_err().field, "timbre_y");
    }

    #[test]
    fn new_with_pressure_z_out_of_range_returns_error() {
        let result = PerNoteExpression::new(0.5, 0.5, -0.5);
        assert!(result.is_err());
        assert_eq!(result.unwrap_err().field, "pressure_z");

        let result = PerNoteExpression::new(0.5, 0.5, 2.0);
        assert!(result.is_err());
        assert_eq!(result.unwrap_err().field, "pressure_z");
    }

    #[test]
    fn is_copy() {
        let a = PerNoteExpression::default();
        let b = a;
        let _ = a;
        let _ = b;
    }
}
