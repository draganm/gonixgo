#include <string>

#include "length.h"

int cxx_length(const char *s) { return static_cast<int>(std::string(s).size()); }
