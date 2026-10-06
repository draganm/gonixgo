#import <Foundation/Foundation.h>

int objc_length(const char *s) {
	@autoreleasepool {
		return (int)[[NSString stringWithUTF8String:s] length];
	}
}
